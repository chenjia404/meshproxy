package meshchatdns

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type dohProvider struct {
	name       string
	endpoint   string
	accept     string
	dialAddr   string
	serverName string
}

type cacheEntry struct {
	ips    []net.IP
	expiry time.Time
}

type dohAnswer struct {
	Data string `json:"data"`
	TTL  int    `json:"TTL"`
	Type int    `json:"type"`
}

type dohResponse struct {
	Status  int         `json:"Status"`
	Answer  []dohAnswer `json:"Answer"`
	Comment string      `json:"Comment"`
}

var (
	cacheMu sync.Mutex
	cache   = map[string]cacheEntry{}

	providers = []dohProvider{
		{
			name:     "cloudflare-1.1.1.1",
			endpoint: "https://1.1.1.1/dns-query",
			accept:   "application/dns-json",
		},
		{
			name:       "aliyun-223.5.5.5",
			endpoint:   "https://dns.alidns.com/resolve",
			accept:     "application/json",
			dialAddr:   "223.5.5.5:443",
			serverName: "dns.alidns.com",
		},
	}
)

func NewHTTPClient(timeout time.Duration) *http.Client {
	transport := cloneDefaultTransport()
	transport.DialContext = dialContext
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

func NewWebSocketDialer(baseURL string, timeout time.Duration) *websocket.Dialer {
	dialer := &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: timeout,
		NetDialContext:   dialContext,
	}
	if u, err := url.Parse(strings.TrimSpace(baseURL)); err == nil {
		if host := strings.TrimSpace(u.Hostname()); host != "" && net.ParseIP(host) == nil {
			dialer.TLSClientConfig = &tls.Config{ServerName: host}
		}
	}
	return dialer
}

func cloneDefaultTransport() *http.Transport {
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		return base.Clone()
	}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

func dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}
	if net.ParseIP(host) != nil {
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}
	ips, err := lookupIPs(ctx, host)
	if err != nil {
		return nil, err
	}
	var (
		d       net.Dialer
		lastErr error
	)
	for _, ip := range ips {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("meshchat doh dial %s: no reachable address", host)
	}
	return nil, lastErr
}

func lookupIPs(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	if cached := getCached(host); len(cached) > 0 {
		return cached, nil
	}
	systemCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	systemAddrs, systemErr := net.DefaultResolver.LookupIPAddr(systemCtx, host)
	if len(systemAddrs) > 0 {
		ips := make([]net.IP, 0, len(systemAddrs))
		for _, item := range systemAddrs {
			if item.IP != nil {
				ips = append(ips, item.IP)
			}
		}
		ips = uniqueIPs(ips)
		if len(ips) > 0 {
			storeCache(host, ips, time.Minute)
			return ips, nil
		}
	}
	var firstErr error
	if systemErr != nil {
		firstErr = systemErr
	}
	for _, provider := range providers {
		ips, ttl, err := provider.lookup(ctx, host)
		if err == nil && len(ips) > 0 {
			storeCache(host, ips, ttl)
			if systemErr != nil {
				log.Printf("[meshchat-dns] host=%s system lookup failed, fallback to DoH provider=%s", host, provider.name)
			}
			return ips, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("meshchat doh resolve %s: no answer", host)
	}
	return nil, firstErr
}

func (p dohProvider) lookup(ctx context.Context, host string) ([]net.IP, time.Duration, error) {
	var (
		allIPs   []net.IP
		bestTTL  time.Duration
		firstErr error
	)
	for _, qtype := range []string{"A", "AAAA"} {
		ips, ttl, err := p.query(ctx, host, qtype)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		allIPs = append(allIPs, ips...)
		if ttl > 0 && (bestTTL == 0 || ttl < bestTTL) {
			bestTTL = ttl
		}
	}
	allIPs = uniqueIPs(allIPs)
	if len(allIPs) == 0 {
		if firstErr == nil {
			firstErr = fmt.Errorf("meshchat doh provider %s: no records for %s", p.name, host)
		}
		return nil, 0, firstErr
	}
	if bestTTL <= 0 {
		bestTTL = 5 * time.Minute
	}
	return allIPs, bestTTL, nil
}

func (p dohProvider) query(ctx context.Context, host, qtype string) ([]net.IP, time.Duration, error) {
	endpoint, err := url.Parse(p.endpoint)
	if err != nil {
		return nil, 0, err
	}
	query := endpoint.Query()
	query.Set("name", host)
	query.Set("type", qtype)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	if p.accept != "" {
		req.Header.Set("Accept", p.accept)
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("meshchat doh provider %s: %s", p.name, resp.Status)
	}
	return decodeAnswerIPs(body, qtype)
}

func (p dohProvider) client() *http.Client {
	transport := cloneDefaultTransport()
	if p.dialAddr != "" {
		transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, p.dialAddr)
		}
	}
	if p.serverName != "" {
		transport.TLSClientConfig = &tls.Config{ServerName: p.serverName}
	}
	return &http.Client{
		Timeout:   6 * time.Second,
		Transport: transport,
	}
}

func decodeAnswerIPs(body []byte, qtype string) ([]net.IP, time.Duration, error) {
	var resp dohResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, 0, err
	}
	if resp.Status != 0 {
		return nil, 0, fmt.Errorf("dns status=%d comment=%s", resp.Status, strings.TrimSpace(resp.Comment))
	}
	wantType := 1
	if strings.EqualFold(qtype, "AAAA") {
		wantType = 28
	}
	var (
		ips     []net.IP
		bestTTL time.Duration
	)
	for _, answer := range resp.Answer {
		if answer.Type != wantType {
			continue
		}
		ip := net.ParseIP(strings.TrimSpace(answer.Data))
		if ip == nil {
			continue
		}
		ips = append(ips, ip)
		ttl := time.Duration(answer.TTL) * time.Second
		if ttl > 0 && (bestTTL == 0 || ttl < bestTTL) {
			bestTTL = ttl
		}
	}
	if len(ips) == 0 {
		return nil, 0, fmt.Errorf("no %s answers", strings.ToUpper(qtype))
	}
	if bestTTL <= 0 {
		bestTTL = 5 * time.Minute
	}
	return uniqueIPs(ips), bestTTL, nil
}

func uniqueIPs(items []net.IP) []net.IP {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]net.IP, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		key := item.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func getCached(host string) []net.IP {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	item, ok := cache[host]
	if !ok || time.Now().After(item.expiry) {
		if ok {
			delete(cache, host)
		}
		return nil
	}
	out := make([]net.IP, len(item.ips))
	copy(out, item.ips)
	return out
}

func storeCache(host string, ips []net.IP, ttl time.Duration) {
	if len(ips) == 0 {
		return
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if ttl > 30*time.Minute {
		ttl = 30 * time.Minute
	}
	cloned := make([]net.IP, len(ips))
	copy(cloned, ips)
	cacheMu.Lock()
	cache[host] = cacheEntry{
		ips:    cloned,
		expiry: time.Now().Add(ttl),
	}
	cacheMu.Unlock()
}
