package ipfsnode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

	httpMirrorGateway              string
	httpClient                     *http.Client
	fetchFromHTTPMirrorFn          func(ctx context.Context, target cid.Cid, fileOnly bool, mirrorURL string) error
	fetchCARSubpathFromHTTPMirrorFn func(ctx context.Context, target cid.Cid, subpath string, mirrorURL string) error
	fetchFromP2PFn                 func(ctx context.Context, c cid.Cid, fileOnly bool) error
}

// NewEmbeddedIPFS 使用既有 host 與 routing（DHT）建立嵌入式 IPFS；baseIPFSDir 通常為 $DataDir/ipfs。
// rt 为 nil 时进入离线模式（无 bitswap），仅支持 HTTP 镜像 + 本地缓存。
func NewEmbeddedIPFS(ctx context.Context, h host.Host, rt routing.Routing, baseIPFSDir string, cfg config.IPFSConfig) (*EmbeddedIPFS, error) {
	dsPath := filepath.Join(baseIPFSDir, "datastore")
	ds, closeDS, err := ipfsstore.OpenLevelDB(dsPath)
	if err != nil {
		return nil, err
	}
	bs := ipfsstore.NewBlockstore(ds)

	var bswapEx *bitswap.Bitswap
	var bsvc blockservice.BlockService
	if rt != nil {
		net := bsnet.NewFromIpfsHost(h)
		bswapEx = bitswap.New(ctx, net, rt, bs)
		bsvc = blockservice.New(bs, bswapEx)
	} else {
		bsvc = blockservice.New(bs, nil)
	}
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
	svc.fetchFromHTTPMirrorFn = svc.fetchFromHTTPMirror
	svc.fetchCARSubpathFromHTTPMirrorFn = svc.fetchCARFromHTTPMirrorWithSubpath
	if rt != nil {
		svc.fetchFromP2PFn = svc.fetchFromP2P
	} else {
		svc.fetchFromP2PFn = func(ctx context.Context, c cid.Cid, fileOnly bool) error {
			return fmt.Errorf("ipfs: P2P disabled (offline mode)")
		}
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

// EnsureLocal 會在本地缺少該 CID 時，若已配置鏡像則並發嘗試 HTTP 鏡像與 P2P（bitswap），先成功者為準；無鏡像時直接走 P2P。
func (e *EmbeddedIPFS) EnsureLocal(ctx context.Context, c cid.Cid) error {
	if e == nil || e.svc == nil {
		return errors.New("ipfs service not initialized")
	}
	return e.svc.ensureLocal(ctx, c, false, "")
}

// EnsureLocalFileOnly 會在本地缺少該 CID 時，若已配置鏡像則並發嘗試鏡像單檔拉取與 P2P（先成功優先）；無鏡像時直接走 P2P。
func (e *EmbeddedIPFS) EnsureLocalFileOnly(ctx context.Context, c cid.Cid) error {
	if e == nil || e.svc == nil {
		return errors.New("ipfs service not initialized")
	}
	return e.svc.ensureLocal(ctx, c, true, "")
}

// EnsureLocalWithMirror 使用請求指定的鏡像，並與 P2P 並發拉取（先成功優先）。
func (e *EmbeddedIPFS) EnsureLocalWithMirror(ctx context.Context, c cid.Cid, mirrorURL string) error {
	if e == nil || e.svc == nil {
		return errors.New("ipfs service not initialized")
	}
	return e.svc.ensureLocal(ctx, c, false, mirrorURL)
}

// EnsureLocalFileOnlyWithMirror 使用請求指定的鏡像按單檔模式，並與 P2P 並發拉取（先成功優先）。
func (e *EmbeddedIPFS) EnsureLocalFileOnlyWithMirror(ctx context.Context, c cid.Cid, mirrorURL string) error {
	if e == nil || e.svc == nil {
		return errors.New("ipfs service not initialized")
	}
	return e.svc.ensureLocal(ctx, c, true, mirrorURL)
}

// EnsureLocalSubpath 當根 CID 為目錄且需解析子路徑時，使用含子路徑的 CAR 從鏡像拉取，與 P2P 並發（先成功優先）。
func (e *EmbeddedIPFS) EnsureLocalSubpath(ctx context.Context, c cid.Cid, subpath string) error {
	if e == nil || e.svc == nil {
		return errors.New("ipfs service not initialized")
	}
	return e.svc.ensureLocalSubpath(ctx, c, subpath, "")
}

// EnsureLocalSubpathWithMirror 使用請求指定的鏡像，當根 CID 為目錄且需解析子路徑時，使用含子路徑的 CAR 拉取，與 P2P 並發（先成功優先）。
func (e *EmbeddedIPFS) EnsureLocalSubpathWithMirror(ctx context.Context, c cid.Cid, subpath string, mirrorURL string) error {
	if e == nil || e.svc == nil {
		return errors.New("ipfs service not initialized")
	}
	return e.svc.ensureLocalSubpath(ctx, c, subpath, mirrorURL)
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

func (s *service) ensureLocalSubpath(ctx context.Context, c cid.Cid, subpath string, mirrorURL string) error {
	if s.isSubpathResolvableLocally(ctx, c, subpath) {
		return nil
	}
	mirrorURL = strings.TrimSpace(mirrorURL)
	if mirrorURL == "" {
		mirrorURL = strings.TrimSpace(s.httpMirrorGateway)
	}
	if mirrorURL == "" {
		return s.fetchFromP2PFn(ctx, c, false)
	}
	return s.ensureLocalSubpathMirrorOrP2P(ctx, c, subpath, mirrorURL)
}

// isSubpathResolvableLocally 使用離線 DAG（不走 bitswap）檢查子路徑上每一段目錄節點和最終目標塊是否都在本地。
func (s *service) isSubpathResolvableLocally(ctx context.Context, root cid.Cid, subpath string) bool {
	offlineBsvc := blockservice.New(s.bs, offlineexchange.Exchange(s.bs))
	offlineDAG := merkledag.NewDAGService(offlineBsvc)

	current := root
	for _, seg := range strings.Split(strings.Trim(subpath, "/"), "/") {
		if seg == "" {
			continue
		}
		nd, err := offlineDAG.Get(ctx, current)
		if err != nil {
			return false
		}
		found := false
		for _, lnk := range nd.Links() {
			if lnk.Name == seg {
				current = lnk.Cid
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	has, err := s.bs.Has(ctx, current)
	return err == nil && has
}

// ensureLocalSubpathMirrorOrP2P 同時從帶子路徑的 CAR 鏡像與 P2P 拉取，先成功者為準。
func (s *service) ensureLocalSubpathMirrorOrP2P(ctx context.Context, c cid.Cid, subpath string, mirrorURL string) error {
	ctxM, cancelM := context.WithCancel(ctx)
	ctxP, cancelP := context.WithCancel(ctx)
	defer cancelM()
	defer cancelP()

	type srcErr struct {
		src string
		err error
	}
	ch := make(chan srcErr, 2)
	go func() {
		ch <- srcErr{"mirror", s.fetchCARSubpathFromHTTPMirrorFn(ctxM, c, subpath, mirrorURL)}
	}()
	go func() {
		ch <- srcErr{"p2p", s.fetchFromP2PFn(ctxP, c, false)}
	}()

	var errs []error
	for i := 0; i < 2; i++ {
		se := <-ch
		if se.err == nil {
			if se.src == "mirror" {
				cancelP()
			} else {
				cancelM()
			}
			return nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", se.src, se.err))
	}
	return errors.Join(errs...)
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
		return s.fetchFromP2PFn(ctx, c, fileOnly)
	}
	return s.ensureLocalMirrorOrP2P(ctx, c, fileOnly, mirrorURL)
}

// ensureLocalMirrorOrP2P 同時從 HTTP 鏡像與 P2P（bitswap）拉取，先成功者為準並取消另一路徑。
func (s *service) ensureLocalMirrorOrP2P(ctx context.Context, c cid.Cid, fileOnly bool, mirrorURL string) error {
	ctxM, cancelM := context.WithCancel(ctx)
	ctxP, cancelP := context.WithCancel(ctx)
	defer cancelM()
	defer cancelP()

	type srcErr struct {
		src string
		err error
	}
	ch := make(chan srcErr, 2)
	go func() {
		ch <- srcErr{"mirror", s.fetchFromHTTPMirrorFn(ctxM, c, fileOnly, mirrorURL)}
	}()
	go func() {
		ch <- srcErr{"p2p", s.fetchFromP2PFn(ctxP, c, fileOnly)}
	}()

	var errs []error
	for i := 0; i < 2; i++ {
		se := <-ch
		if se.err == nil {
			if se.src == "mirror" {
				cancelP()
			} else {
				cancelM()
			}
			return nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", se.src, se.err))
	}
	return errors.Join(errs...)
}

// fetchFromP2P 透過 DAG 服務從網路拉取區塊（bitswap）。fileOnly 時先嘗試 UnixFS 串流讀盡，失敗則再嘗試整棵 DAG。
func (s *service) fetchFromP2P(ctx context.Context, c cid.Cid, fileOnly bool) error {
	if fileOnly {
		errGet := ipfsunixfs.Get(ctx, s.dag, c, io.Discard)
		if errGet == nil {
			s.maybeProvideRoot(ctx, c)
			return nil
		}
		return errGet
	}
	if err := merkledag.FetchGraph(ctx, c, s.dag, merkledag.Concurrent()); err != nil {
		return err
	}
	s.maybeProvideRoot(ctx, c)
	return nil
}

func (s *service) maybeProvideRoot(ctx context.Context, c cid.Cid) {
	if s.cfg.AutoProvide && s.rt != nil {
		_ = s.rt.Provide(ctx, c, true)
	}
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

// fetchCARFromHTTPMirrorWithSubpath 從 HTTP 鏡像以 CAR 格式拉取帶子路徑的內容（只含路徑所需的塊）。
func (s *service) fetchCARFromHTTPMirrorWithSubpath(ctx context.Context, target cid.Cid, subpath string, mirrorURL string) error {
	urlPath := "/ipfs/" + target.String()
	if subpath != "" {
		for _, seg := range strings.Split(strings.Trim(subpath, "/"), "/") {
			if seg == "" {
				continue
			}
			urlPath += "/" + url.PathEscape(seg)
		}
	}
	return s.doFetchCARFromHTTPMirror(ctx, target, mirrorURL, urlPath)
}

func (s *service) fetchCARFromHTTPMirror(ctx context.Context, target cid.Cid, mirrorURL string) error {
	return s.doFetchCARFromHTTPMirror(ctx, target, mirrorURL, "/ipfs/"+target.String())
}

func (s *service) doFetchCARFromHTTPMirror(ctx context.Context, target cid.Cid, mirrorURL string, urlPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(mirrorURL, "/")+urlPath+"?format=car", nil)
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
