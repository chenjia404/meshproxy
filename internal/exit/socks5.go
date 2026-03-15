package exit

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"

	"github.com/chenjia404/meshproxy/internal/protocol"
	"github.com/chenjia404/meshproxy/internal/safe"
)

// Socks5Server is a direct-outbound SOCKS5 proxy for exit nodes.
// It enforces the exit policy but does not create circuits.
type Socks5Server struct {
	addr      string
	policy    *PolicyChecker
	listener  net.Listener
	closeChan chan struct{}
}

func NewSocks5Server(addr string, policy *PolicyChecker) *Socks5Server {
	return &Socks5Server{
		addr:      addr,
		policy:    policy,
		closeChan: make(chan struct{}),
	}
}

func (s *Socks5Server) Start() error {
	l, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = l
	log.Printf("[exit-socks5] listening on %s", s.addr)
	safe.Go("exit.socks5.acceptLoop", s.acceptLoop)
	return nil
}

func (s *Socks5Server) Close() error {
	close(s.closeChan)
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Socks5Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.closeChan:
				return
			default:
			}
			log.Printf("[exit-socks5] accept error: %v", err)
			continue
		}
		safe.Go("exit.socks5.handleConn", func() { s.handleConn(conn) })
	}
}

func (s *Socks5Server) handleConn(conn net.Conn) {
	defer conn.Close()

	host, port, cmd, err := s.handleHandshake(conn)
	if err != nil {
		log.Printf("[exit-socks5] handshake failed: %v", err)
		return
	}
	if cmd != 0x01 {
		_ = sendExitSocks5Reply(conn, 0x07)
		return
	}

	begin := &protocol.BeginTCP{TargetHost: host, TargetPort: int(port)}
	if rejectReason := s.checkBeginPolicyLocal(begin); rejectReason != "" {
		log.Printf("[exit-socks5] denied target=%s:%d reason=%s", host, port, rejectReason)
		_ = sendExitSocks5Reply(conn, 0x02)
		return
	}

	remote, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		log.Printf("[exit-socks5] dial %s:%d failed: %v", host, port, err)
		_ = sendExitSocks5Reply(conn, 0x05)
		return
	}
	defer remote.Close()

	if err := sendExitSocks5Reply(conn, 0x00); err != nil {
		return
	}

	var once sync.Once
	closeBoth := func() {
		_ = conn.Close()
		_ = remote.Close()
	}

	safe.Go("exit.socks5.copyUp", func() {
		_, _ = io.Copy(remote, conn)
		once.Do(closeBoth)
	})
	_, _ = io.Copy(conn, remote)
	once.Do(closeBoth)
}

func (s *Socks5Server) checkBeginPolicyLocal(begin *protocol.BeginTCP) ExitRejectReason {
	if s.policy == nil {
		return ""
	}
	p := s.policy
	if !p.IsEnabled() {
		return ExitRejectDisabled
	}
	if !p.AcceptNewStreams() || p.DrainMode() {
		return ExitRejectDraining
	}
	if reason, ok := p.CheckProtocolAllowed(true); !ok {
		return reason
	}
	if reason, ok := p.CheckPortAllowed(begin.TargetPort); !ok {
		return reason
	}
	host := begin.TargetHost
	ip := net.ParseIP(host)
	if ip != nil {
		if reason, ok := p.CheckTargetIPAllowed(ip); !ok {
			return reason
		}
		return ""
	}
	if reason, ok := p.CheckDomainAllowed(host); !ok {
		return reason
	}
	if !p.GetPolicy().RemoteDNS {
		return ""
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return ExitRejectDomainDenied
	}
	for _, a := range addrs {
		if reason, ok := p.CheckTargetIPAllowed(a); !ok {
			return reason
		}
	}
	return ""
}

func (s *Socks5Server) handleHandshake(conn net.Conn) (string, uint16, byte, error) {
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return "", 0, 0, err
	}
	if buf[0] != 0x05 {
		return "", 0, 0, fmt.Errorf("unsupported version %d", buf[0])
	}
	nMethods := int(buf[1])
	methods := make([]byte, nMethods)
	if _, err := io.ReadFull(conn, methods); err != nil {
		return "", 0, 0, err
	}
	supportNoAuth := false
	for _, m := range methods {
		if m == 0x00 {
			supportNoAuth = true
			break
		}
	}
	if !supportNoAuth {
		_, _ = conn.Write([]byte{0x05, 0xFF})
		return "", 0, 0, fmt.Errorf("no acceptable auth method")
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return "", 0, 0, err
	}

	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", 0, 0, err
	}
	if header[0] != 0x05 {
		return "", 0, 0, fmt.Errorf("invalid request version %d", header[0])
	}
	cmd := header[1]
	if cmd != 0x01 && cmd != 0x03 {
		_ = sendExitSocks5Reply(conn, 0x07)
		return "", 0, 0, fmt.Errorf("unsupported cmd %d", cmd)
	}

	atyp := header[3]
	var host string
	switch atyp {
	case 0x01:
		addr := make([]byte, 4)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return "", 0, 0, err
		}
		host = net.IP(addr).String()
	case 0x03:
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return "", 0, 0, err
		}
		dlen := int(buf[0])
		domain := make([]byte, dlen)
		if _, err := io.ReadFull(conn, domain); err != nil {
			return "", 0, 0, err
		}
		host = string(domain)
	case 0x04:
		addr := make([]byte, 16)
		if _, err := io.ReadFull(conn, addr); err != nil {
			return "", 0, 0, err
		}
		host = net.IP(addr).String()
	default:
		_ = sendExitSocks5Reply(conn, 0x08)
		return "", 0, 0, fmt.Errorf("unsupported atyp %d", atyp)
	}
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return "", 0, 0, err
	}
	port := binary.BigEndian.Uint16(buf[:2])
	return host, port, cmd, nil
}

func sendExitSocks5Reply(conn net.Conn, rep byte) error {
	resp := []byte{
		0x05,
		rep,
		0x00,
		0x01,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00,
	}
	_, err := conn.Write(resp)
	return err
}
