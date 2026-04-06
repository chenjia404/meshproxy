package ipfsnode

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ipfs/boxo/blockservice"
	blockstore "github.com/ipfs/boxo/blockstore"
	offlineexchange "github.com/ipfs/boxo/exchange/offline"
	"github.com/ipfs/boxo/ipld/merkledag"
	cid "github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	carblockstore "github.com/ipld/go-car/v2/blockstore"

	"github.com/chenjia404/meshproxy/internal/config"
	"github.com/chenjia404/meshproxy/internal/ipfspin"
	"github.com/chenjia404/meshproxy/internal/ipfsunixfs"
)

func TestServicePinFetchesFromHTTPMirrorCAR(t *testing.T) {
	cfg := config.Default().IPFS
	cfg.AutoProvide = false

	data := []byte("hello ipfs car mirror")
	target := mustCIDForConfig(t, cfg, data)
	carBytes := mustCARForConfig(t, cfg, data)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ipfs/"+target {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("format") != "car" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.ipld.car")
		_, _ = w.Write(carBytes)
	}))
	defer srv.Close()

	cfg.HTTPMirrorGateway = srv.URL
	svc := newTestService(t, cfg)

	c := mustDecodeCID(t, target)
	if err := svc.Pin(context.Background(), c, true); err != nil {
		t.Fatalf("Pin should fetch car from mirror: %v", err)
	}

	local, err := svc.HasLocal(context.Background(), c)
	if err != nil {
		t.Fatalf("HasLocal after pin: %v", err)
	}
	if !local {
		t.Fatalf("expected cid to be present locally after car mirror fetch")
	}
}

func TestEmbeddedIPFSEnsureLocalFileOnlySkipsCAR(t *testing.T) {
	cfg := config.Default().IPFS
	cfg.AutoProvide = false

	data := []byte("hello ipfs file-only mirror")
	target := mustCIDForConfig(t, cfg, data)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ipfs/"+target {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("format") == "car" {
			http.Error(w, "car disabled", http.StatusNotFound)
			return
		}
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	cfg.HTTPMirrorGateway = srv.URL
	svc := newTestService(t, cfg)
	e := &EmbeddedIPFS{svc: svc}

	c := mustDecodeCID(t, target)
	if err := e.EnsureLocalFileOnly(context.Background(), c); err != nil {
		t.Fatalf("EnsureLocalFileOnly should fetch plain file from mirror: %v", err)
	}

	local, err := svc.HasLocal(context.Background(), c)
	if err != nil {
		t.Fatalf("HasLocal after file-only ensure: %v", err)
	}
	if !local {
		t.Fatalf("expected cid to be present locally after file-only ensure")
	}
}

func TestServicePinFetchesFromHTTPMirror(t *testing.T) {
	cfg := config.Default().IPFS
	cfg.AutoProvide = false

	data := []byte("hello ipfs http mirror")
	target := mustCIDForConfig(t, cfg, data)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ipfs/"+target {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	cfg.HTTPMirrorGateway = srv.URL
	svc := newTestService(t, cfg)

	c := mustDecodeCID(t, target)
	local, err := svc.HasLocal(context.Background(), c)
	if err != nil {
		t.Fatalf("HasLocal before pin: %v", err)
	}
	if local {
		t.Fatalf("expected cid to be missing locally before mirror fetch")
	}

	if err := svc.Pin(context.Background(), c, true); err != nil {
		t.Fatalf("Pin should fetch from mirror: %v", err)
	}

	local, err = svc.HasLocal(context.Background(), c)
	if err != nil {
		t.Fatalf("HasLocal after pin: %v", err)
	}
	if !local {
		t.Fatalf("expected cid to be present locally after mirror fetch")
	}

	st, err := svc.Stat(context.Background(), c)
	if err != nil {
		t.Fatalf("Stat after mirror fetch: %v", err)
	}
	if st.Size != int64(len(data)) {
		t.Fatalf("Stat size = %d, want %d", st.Size, len(data))
	}
	if !st.Pinned {
		t.Fatalf("expected cid to be pinned after Pin")
	}
}

func TestServiceEnsureLocalRejectsCIDMismatch(t *testing.T) {
	cfg := config.Default().IPFS
	cfg.AutoProvide = false

	wantData := []byte("expected content")
	target := mustCIDForConfig(t, cfg, wantData)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("wrong content"))
	}))
	defer srv.Close()

	cfg.HTTPMirrorGateway = srv.URL
	svc := newTestService(t, cfg)

	c := mustDecodeCID(t, target)
	err := svc.ensureLocal(context.Background(), c, false)
	if !errors.Is(err, ErrMirrorCIDMismatch) {
		t.Fatalf("ensureLocal error = %v, want ErrMirrorCIDMismatch", err)
	}

	local, hasErr := svc.HasLocal(context.Background(), c)
	if hasErr != nil {
		t.Fatalf("HasLocal after mismatch: %v", hasErr)
	}
	if local {
		t.Fatalf("cid should not be stored locally after mirror mismatch")
	}
}

func newTestService(t *testing.T, cfg config.IPFSConfig) *service {
	t.Helper()

	ds := dssync.MutexWrap(datastore.NewMapDatastore())
	bs := blockstore.NewBlockstore(ds)
	bsvc := blockservice.New(bs, offlineexchange.Exchange(bs))
	dag := merkledag.NewDAGService(bsvc)
	pins, err := ipfspin.NewFileStore(filepath.Join(t.TempDir(), "pins.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return &service{
		cfg:               cfg,
		bs:                bs,
		bsvc:              bsvc,
		dag:               dag,
		pins:              pins,
		httpMirrorGateway: cfg.HTTPMirrorGateway,
		httpClient:        http.DefaultClient,
	}
}

func mustCIDForConfig(t *testing.T, cfg config.IPFSConfig, data []byte) string {
	t.Helper()

	_, dag := newEphemeralDAG()
	nd, err := ipfsunixfs.AddFileFromReader(
		context.Background(),
		dag,
		bytes.NewReader(data),
		ipfsunixfs.ChunkSizeFromSpec(cfg.Chunker),
		cfg.RawLeaves,
		cfg.CIDVersion,
		cfg.HashFunction,
	)
	if err != nil {
		t.Fatalf("AddFileFromReader: %v", err)
	}
	return nd.Cid().String()
}

func mustCARForConfig(t *testing.T, cfg config.IPFSConfig, data []byte) []byte {
	t.Helper()

	_, dag := newEphemeralDAG()
	nd, err := ipfsunixfs.AddFileFromReader(
		context.Background(),
		dag,
		bytes.NewReader(data),
		ipfsunixfs.ChunkSizeFromSpec(cfg.Chunker),
		cfg.RawLeaves,
		cfg.CIDVersion,
		cfg.HashFunction,
	)
	if err != nil {
		t.Fatalf("AddFileFromReader for CAR: %v", err)
	}
	reachable, err := collectReachableBlocks(context.Background(), dag, nd.Cid())
	if err != nil {
		t.Fatalf("collectReachableBlocks for CAR: %v", err)
	}
	carPath := filepath.Join(t.TempDir(), "fixture.car")
	rw, err := carblockstore.OpenReadWrite(carPath, []cid.Cid{nd.Cid()})
	if err != nil {
		t.Fatalf("OpenReadWrite CAR: %v", err)
	}
	if err := rw.PutMany(context.Background(), reachable); err != nil {
		_ = rw.Close()
		t.Fatalf("PutMany CAR: %v", err)
	}
	if err := rw.Finalize(); err != nil {
		_ = rw.Close()
		t.Fatalf("Finalize CAR: %v", err)
	}
	_ = rw.Close()
	f, err := os.Open(carPath)
	if err != nil {
		t.Fatalf("open car file: %v", err)
	}
	defer f.Close()
	out, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read car file: %v", err)
	}
	return out
}

func mustDecodeCID(t *testing.T, value string) cid.Cid {
	t.Helper()

	c, err := cid.Decode(value)
	if err != nil {
		t.Fatalf("cid.Decode(%q): %v", value, err)
	}
	return c
}
