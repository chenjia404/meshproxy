package ipfsnode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ipfs/boxo/bitswap"
	bsnet "github.com/ipfs/boxo/bitswap/network/bsnet"
	"github.com/ipfs/boxo/blockservice"
	blockstore "github.com/ipfs/boxo/blockstore"
	offlineexchange "github.com/ipfs/boxo/exchange/offline"
	"github.com/ipfs/boxo/ipld/merkledag"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	carv2 "github.com/ipld/go-car/v2"
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

// ErrMirrorCIDMismatch 表示 HTTP 鏡像閘道返回的內容經 UnixFS 導入後 CID 與目標不一致。
var ErrMirrorCIDMismatch = errors.New("ipfs mirror content cid mismatch")

// EmbeddedIPFS 綁定共享 libp2p host 的 IPFS 子系統。
type EmbeddedIPFS struct {
	svc             *service
	closeDS         func() error
	bswap           *bitswap.Bitswap
	gw              http.Handler
	fetchTimeout    time.Duration
	maxUploadBytes  int64
	gatewayEnabled  bool
	gatewayWritable bool
	apiEnabled      bool
	autoPinOnAdd    bool
	cidVersion      int
	hashFunction    string
	chunker         string
	rawLeaves       bool
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

// FetchTimeout returns the configured timeout used by gateway and mirror fetches.
func (e *EmbeddedIPFS) FetchTimeout() time.Duration {
	if e == nil || e.fetchTimeout <= 0 {
		return 60 * time.Second
	}
	return e.fetchTimeout
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

// CIDVersion returns the default CID version used for file imports.
func (e *EmbeddedIPFS) CIDVersion() int {
	if e == nil || e.cidVersion <= 0 {
		return 1
	}
	return e.cidVersion
}

// HashFunction returns the default multihash function used for file imports.
func (e *EmbeddedIPFS) HashFunction() string {
	if e == nil || e.hashFunction == "" {
		return "sha2-256"
	}
	return e.hashFunction
}

// Chunker returns the default chunker spec used for file imports.
func (e *EmbeddedIPFS) Chunker() string {
	if e == nil || e.chunker == "" {
		return "size-1048576"
	}
	return e.chunker
}

// RawLeaves returns the default raw-leaves setting used for file imports.
func (e *EmbeddedIPFS) RawLeaves() bool {
	if e == nil {
		return true
	}
	return e.rawLeaves
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

	httpMirrorGateway string
	httpClient        *http.Client
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
		cfg:               cfg,
		rt:                rt,
		bs:                bs,
		bswap:             bswapEx,
		bsvc:              bsvc,
		dag:               dag,
		pins:              pinStore,
		httpMirrorGateway: cfg.HTTPMirrorGateway,
		httpClient: &http.Client{
			Timeout: ft,
		},
	}
	return &EmbeddedIPFS{
		svc:             svc,
		closeDS:         closeDS,
		bswap:           bswapEx,
		gw:              gw,
		fetchTimeout:    ft,
		maxUploadBytes:  cfg.MaxUploadBytes,
		gatewayEnabled:  cfg.GatewayEnabled,
		gatewayWritable: cfg.GatewayWritable,
		apiEnabled:      cfg.APIEnabled,
		autoPinOnAdd:    cfg.AutoPinOnAdd,
		cidVersion:      cfg.CIDVersion,
		hashFunction:    cfg.HashFunction,
		chunker:         cfg.Chunker,
		rawLeaves:       cfg.RawLeaves,
	}, nil
}

// EnsureLocal 會在本地缺少該 CID 時，嘗試從配置的 HTTP 鏡像閘道拉取並校驗後寫入本地 blockstore。
func (e *EmbeddedIPFS) EnsureLocal(ctx context.Context, c cid.Cid) error {
	if e == nil || e.svc == nil {
		return errors.New("ipfs service not initialized")
	}
	return e.svc.ensureLocal(ctx, c, false, "")
}

// EnsureLocalFileOnly 會在本地缺少該 CID 時，僅按單檔內容從 HTTP 鏡像閘道拉取並校驗。
func (e *EmbeddedIPFS) EnsureLocalFileOnly(ctx context.Context, c cid.Cid) error {
	if e == nil || e.svc == nil {
		return errors.New("ipfs service not initialized")
	}
	return e.svc.ensureLocal(ctx, c, true, "")
}

// EnsureLocalWithMirror 允許用單次請求指定的鏡像位址補拉內容。
func (e *EmbeddedIPFS) EnsureLocalWithMirror(ctx context.Context, c cid.Cid, mirrorURL string) error {
	if e == nil || e.svc == nil {
		return errors.New("ipfs service not initialized")
	}
	return e.svc.ensureLocal(ctx, c, false, mirrorURL)
}

// EnsureLocalFileOnlyWithMirror 允許用單次請求指定的鏡像位址按單檔模式補拉內容。
func (e *EmbeddedIPFS) EnsureLocalFileOnlyWithMirror(ctx context.Context, c cid.Cid, mirrorURL string) error {
	if e == nil || e.svc == nil {
		return errors.New("ipfs service not initialized")
	}
	return e.svc.ensureLocal(ctx, c, true, mirrorURL)
}

func (s *service) AddFile(ctx context.Context, r io.Reader, opt AddFileOptions) (cid.Cid, error) {
	chunkSize := ipfsunixfs.ChunkSizeFromSpec(opt.Chunker)
	if chunkSize <= 0 {
		chunkSize = 1048576
	}
	nd, err := ipfsunixfs.AddFileFromReader(
		ctx,
		s.dag,
		r,
		chunkSize,
		opt.RawLeaves,
		opt.CIDVersion,
		opt.HashFunction,
	)
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
	if err := s.ensureLocal(ctx, c, false, ""); err != nil {
		return nil, err
	}
	return ipfsunixfs.Cat(ctx, s.dag, c)
}

func (s *service) Get(ctx context.Context, c cid.Cid, w io.Writer) error {
	if err := s.ensureLocal(ctx, c, false, ""); err != nil {
		return err
	}
	return ipfsunixfs.Get(ctx, s.dag, c, w)
}

func (s *service) Stat(ctx context.Context, c cid.Cid) (*ObjectStat, error) {
	if err := s.ensureLocal(ctx, c, false, ""); err != nil {
		return nil, err
	}
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
	if !recursive {
		return ipfspin.ErrNotImplemented
	}
	if err := s.ensureLocal(ctx, c, false, ""); err != nil {
		return err
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

func (s *service) ensureLocal(ctx context.Context, c cid.Cid, fileOnly bool, mirrorURL string) error {
	local, err := s.bs.Has(ctx, c)
	if err != nil {
		return err
	}
	if local {
		return nil
	}
	mirrorURL = strings.TrimSpace(mirrorURL)
	if mirrorURL == "" {
		mirrorURL = strings.TrimSpace(s.httpMirrorGateway)
	}
	if mirrorURL == "" {
		return nil
	}
	return s.fetchFromHTTPMirror(ctx, c, fileOnly, mirrorURL)
}

func (s *service) fetchFromHTTPMirror(ctx context.Context, target cid.Cid, fileOnly bool, mirrorURL string) error {
	if !fileOnly {
		if err := s.fetchCARFromHTTPMirror(ctx, target, mirrorURL); err == nil {
			return nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(mirrorURL, "/")+"/ipfs/"+target.String(), nil)
	if err != nil {
		return err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch from ipfs http mirror: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ipfs http mirror returned status %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "meshproxy-ipfs-mirror-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return fmt.Errorf("copy mirror response: %w", err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}

	verified, err := s.importIntoTempDAG(tmp)
	if err != nil {
		return fmt.Errorf("verify mirror content cid: %w", err)
	}
	if !verified.Equals(target) {
		return fmt.Errorf("%w: want %s got %s", ErrMirrorCIDMismatch, target.String(), verified.String())
	}

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	nd, err := ipfsunixfs.AddFileFromReader(
		ctx,
		s.dag,
		tmp,
		ipfsunixfs.ChunkSizeFromSpec(s.cfg.Chunker),
		s.cfg.RawLeaves,
		s.cfg.CIDVersion,
		s.cfg.HashFunction,
	)
	if err != nil {
		return fmt.Errorf("store mirror content locally: %w", err)
	}
	if !nd.Cid().Equals(target) {
		return fmt.Errorf("%w: want %s got %s", ErrMirrorCIDMismatch, target.String(), nd.Cid().String())
	}
	if s.cfg.AutoProvide && s.rt != nil {
		_ = s.rt.Provide(ctx, target, true)
	}
	return nil
}

func (s *service) fetchCARFromHTTPMirror(ctx context.Context, target cid.Cid, mirrorURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(mirrorURL, "/")+"/ipfs/"+target.String()+"?format=car", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.ipld.car")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch car from ipfs http mirror: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ipfs http mirror returned car status %d", resp.StatusCode)
	}

	tempBS, tempDAG := newEphemeralDAG()
	br, err := carv2.NewBlockReader(resp.Body)
	if err != nil {
		return fmt.Errorf("read car from ipfs http mirror: %w", err)
	}
	for {
		blk, nextErr := br.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("iterate car blocks: %w", nextErr)
		}
		if err := tempBS.Put(ctx, blk); err != nil {
			return fmt.Errorf("store car block in temp blockstore: %w", err)
		}
	}

	ok, err := tempBS.Has(ctx, target)
	if err != nil {
		return fmt.Errorf("check target root in temp blockstore: %w", err)
	}
	if !ok {
		return fmt.Errorf("car from ipfs http mirror missing target root %s", target.String())
	}

	visited, err := collectReachableBlocks(ctx, tempDAG, target)
	if err != nil {
		return fmt.Errorf("validate car dag from root %s: %w", target.String(), err)
	}
	if len(visited) == 0 {
		return fmt.Errorf("car from ipfs http mirror contained no reachable blocks for %s", target.String())
	}
	if err := s.bs.PutMany(ctx, visited); err != nil {
		return fmt.Errorf("store car blocks locally: %w", err)
	}
	if s.cfg.AutoProvide && s.rt != nil {
		_ = s.rt.Provide(ctx, target, true)
	}
	return nil
}

func (s *service) importIntoTempDAG(r io.Reader) (cid.Cid, error) {
	_, dag := newEphemeralDAG()
	nd, err := ipfsunixfs.AddFileFromReader(
		context.Background(),
		dag,
		r,
		ipfsunixfs.ChunkSizeFromSpec(s.cfg.Chunker),
		s.cfg.RawLeaves,
		s.cfg.CIDVersion,
		s.cfg.HashFunction,
	)
	if err != nil {
		return cid.Undef, err
	}
	return nd.Cid(), nil
}

func newEphemeralDAG() (blockstore.Blockstore, ipld.DAGService) {
	ds := dssync.MutexWrap(datastore.NewMapDatastore())
	bs := blockstore.NewBlockstore(ds)
	bsvc := blockservice.New(bs, offlineexchange.Exchange(bs))
	return bs, merkledag.NewDAGService(bsvc)
}

func collectReachableBlocks(ctx context.Context, dag ipld.DAGService, root cid.Cid) ([]blocks.Block, error) {
	out := make([]blocks.Block, 0, 16)
	seen := make(map[string]struct{})
	err := merkledag.Walk(ctx, merkledag.GetLinksDirect(dag), root, func(c cid.Cid) bool {
		key := c.KeyString()
		if _, ok := seen[key]; ok {
			return false
		}
		seen[key] = struct{}{}
		blk, err := dag.Get(ctx, c)
		if err != nil {
			return false
		}
		out = append(out, blk)
		return true
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
