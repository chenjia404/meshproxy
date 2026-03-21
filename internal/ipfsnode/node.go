package ipfsnode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/ipfs/boxo/bitswap"
	bsnet "github.com/ipfs/boxo/bitswap/network/bsnet"
	"github.com/ipfs/boxo/blockservice"
	blockstore "github.com/ipfs/boxo/blockstore"
	"github.com/ipfs/boxo/ipld/merkledag"
	host "github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/routing"

	"github.com/chenjia404/meshproxy/internal/config"
	"github.com/chenjia404/meshproxy/internal/ipfsgateway"
	"github.com/chenjia404/meshproxy/internal/ipfspin"
	"github.com/chenjia404/meshproxy/internal/ipfsstore"
	"github.com/chenjia404/meshproxy/internal/ipfsunixfs"

	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
)

// ErrNotImplemented 目錄匯入等尚未實作時使用。
var ErrNotImplemented = errors.New("not implemented")

// EmbeddedIPFS 綁定共享 libp2p host 的 IPFS 子系統。
type EmbeddedIPFS struct {
	svc            *service
	closeDS        func() error
	bswap          *bitswap.Bitswap
	gw             http.Handler
	maxUploadBytes int64
	gatewayEnabled  bool
	gatewayWritable bool
	apiEnabled      bool
	autoPinOnAdd    bool
}

// Service 實作 IPFSService。
func (e *EmbeddedIPFS) Service() IPFSService {
	return e.svc
}

// GatewayHandler 提供 GET /ipfs/{cid}（應掛在 /ipfs/）。
func (e *EmbeddedIPFS) GatewayHandler() http.Handler {
	return e.gw
}

// MaxUploadBytes POST /api/ipfs/add 單次上傳上限。
func (e *EmbeddedIPFS) MaxUploadBytes() int64 {
	if e == nil || e.maxUploadBytes <= 0 {
		return 64 << 20
	}
	return e.maxUploadBytes
}

// GatewayEnabled 是否註冊 GET /ipfs/。
func (e *EmbeddedIPFS) GatewayEnabled() bool {
	return e != nil && e.gatewayEnabled
}

// APIEnabled 是否註冊 /api/ipfs/* 寫入與 stat、pin。
func (e *EmbeddedIPFS) APIEnabled() bool {
	return e != nil && e.apiEnabled
}

// GatewayWritable 是否允許 POST/PUT 到 /ipfs/ 上傳（與 /api/ipfs/add 相同）。
func (e *EmbeddedIPFS) GatewayWritable() bool {
	return e != nil && e.gatewayWritable
}

// AutoPinOnAdd 對應設定檔 ipfs.auto_pin_on_add。
func (e *EmbeddedIPFS) AutoPinOnAdd() bool {
	return e != nil && e.autoPinOnAdd
}

// Close 釋放 bitswap 與本地 datastore。
func (e *EmbeddedIPFS) Close() error {
	var errs []error
	if e.bswap != nil {
		errs = append(errs, e.bswap.Close())
	}
	if e.closeDS != nil {
		errs = append(errs, e.closeDS())
	}
	return errors.Join(errs...)
}

type service struct {
	cfg   config.IPFSConfig
	rt    routing.Routing
	bs    blockstore.Blockstore
	bswap *bitswap.Bitswap
	bsvc  blockservice.BlockService
	dag   ipld.DAGService
	pins  *ipfspin.FileStore
}

// NewEmbeddedIPFS 使用既有 host 與 routing（DHT）建立嵌入式 IPFS；baseIPFSDir 通常為 $DataDir/ipfs。
func NewEmbeddedIPFS(ctx context.Context, h host.Host, rt routing.Routing, baseIPFSDir string, cfg config.IPFSConfig) (*EmbeddedIPFS, error) {
	if rt == nil {
		return nil, fmt.Errorf("ipfs: routing is required (DHT / ContentDiscovery)")
	}
	dsPath := filepath.Join(baseIPFSDir, "datastore")
	ds, closeDS, err := ipfsstore.OpenLevelDB(dsPath)
	if err != nil {
		return nil, err
	}
	bs := ipfsstore.NewBlockstore(ds)
	net := bsnet.NewFromIpfsHost(h)
	bswapEx := bitswap.New(ctx, net, rt, bs)
	bsvc := blockservice.New(bs, bswapEx)
	dag := merkledag.NewDAGService(bsvc)

	pinsPath := filepath.Join(baseIPFSDir, "pins.json")
	pinStore, err := ipfspin.NewFileStore(pinsPath)
	if err != nil {
		_ = bswapEx.Close()
		_ = closeDS()
		return nil, err
	}

	ft := time.Duration(cfg.FetchTimeoutSeconds) * time.Second
	gw, err := ipfsgateway.NewHandler(ft, bsvc)
	if err != nil {
		_ = bswapEx.Close()
		_ = closeDS()
		return nil, err
	}

	svc := &service{
		cfg:   cfg,
		rt:    rt,
		bs:    bs,
		bswap: bswapEx,
		bsvc:  bsvc,
		dag:   dag,
		pins:  pinStore,
	}
	return &EmbeddedIPFS{
		svc:             svc,
		closeDS:         closeDS,
		bswap:           bswapEx,
		gw:              gw,
		maxUploadBytes:  cfg.MaxUploadBytes,
		gatewayEnabled:  cfg.GatewayEnabled,
		gatewayWritable: cfg.GatewayWritable,
		apiEnabled:      cfg.APIEnabled,
		autoPinOnAdd:    cfg.AutoPinOnAdd,
	}, nil
}

func (s *service) AddFile(ctx context.Context, r io.Reader, opt AddFileOptions) (cid.Cid, error) {
	chunkSize := ipfsunixfs.ChunkSizeFromSpec(opt.Chunker)
	if chunkSize <= 0 {
		chunkSize = 262144
	}
	nd, err := ipfsunixfs.AddFileFromReader(ctx, s.dag, r, chunkSize, opt.RawLeaves)
	if err != nil {
		return cid.Cid{}, err
	}
	c := nd.Cid()
	if opt.Pin {
		if err := s.pins.PinRecursive(c); err != nil {
			return cid.Cid{}, err
		}
	}
	if s.cfg.AutoProvide && s.rt != nil {
		_ = s.rt.Provide(ctx, c, true)
	}
	return c, nil
}

func (s *service) AddDir(ctx context.Context, path string, opt AddDirOptions) (cid.Cid, error) {
	return cid.Cid{}, ErrNotImplemented
}

func (s *service) Cat(ctx context.Context, c cid.Cid) (io.ReadCloser, error) {
	return ipfsunixfs.Cat(ctx, s.dag, c)
}

func (s *service) Get(ctx context.Context, c cid.Cid, w io.Writer) error {
	return ipfsunixfs.Get(ctx, s.dag, c, w)
}

func (s *service) Stat(ctx context.Context, c cid.Cid) (*ObjectStat, error) {
	local, err := s.bs.Has(ctx, c)
	if err != nil {
		return nil, err
	}
	st, err := ipfsunixfs.Stat(ctx, s.dag, c)
	if err != nil {
		return nil, err
	}
	return &ObjectStat{
		CID:      c.String(),
		Size:     st.Size,
		NumLinks: st.NumLinks,
		Local:    local,
		Pinned:   s.pins.IsPinnedRecursive(c),
	}, nil
}

func (s *service) Pin(ctx context.Context, c cid.Cid, recursive bool) error {
	_ = ctx
	if !recursive {
		return ipfspin.ErrNotImplemented
	}
	return s.pins.PinRecursive(c)
}

func (s *service) Unpin(ctx context.Context, c cid.Cid, recursive bool) error {
	_ = ctx
	if !recursive {
		return ipfspin.ErrNotImplemented
	}
	return s.pins.UnpinRecursive(c)
}

func (s *service) IsPinned(ctx context.Context, c cid.Cid) (bool, error) {
	_ = ctx
	return s.pins.IsPinnedRecursive(c), nil
}

func (s *service) Provide(ctx context.Context, c cid.Cid, recursive bool) error {
	if s.rt == nil {
		return fmt.Errorf("routing not available")
	}
	_ = recursive
	return s.rt.Provide(ctx, c, true)
}

func (s *service) HasLocal(ctx context.Context, c cid.Cid) (bool, error) {
	return s.bs.Has(ctx, c)
}
