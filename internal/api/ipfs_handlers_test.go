package api

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	cid "github.com/ipfs/go-cid"
)

func TestIPFSMirrorURLFromHeader(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/ipfs/test", nil)
		got, err := ipfsMirrorURLFromHeader(r)
		if err != nil {
			t.Fatalf("ipfsMirrorURLFromHeader empty error: %v", err)
		}
		if got != "" {
			t.Fatalf("ipfsMirrorURLFromHeader empty = %q, want empty", got)
		}
	})

	t.Run("valid host", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/ipfs/test", nil)
		r.Header.Set("http_mirror_gateway", "mirror.example.com:8443")
		got, err := ipfsMirrorURLFromHeader(r)
		if err != nil {
			t.Fatalf("ipfsMirrorURLFromHeader valid error: %v", err)
		}
		if got != "https://mirror.example.com:8443" {
			t.Fatalf("ipfsMirrorURLFromHeader valid = %q", got)
		}
	})

	t.Run("reject scheme", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/ipfs/test", nil)
		r.Header.Set("http_mirror_gateway", "https://mirror.example.com")
		_, err := ipfsMirrorURLFromHeader(r)
		if err == nil {
			t.Fatalf("ipfsMirrorURLFromHeader should reject scheme")
		}
	})

	t.Run("reject path", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/ipfs/test", nil)
		r.Header.Set("http_mirror_gateway", "mirror.example.com/ipfs")
		_, err := ipfsMirrorURLFromHeader(r)
		if err == nil {
			t.Fatalf("ipfsMirrorURLFromHeader should reject path")
		}
	})
}

type fakeIPFSMirrorEnsurer struct {
	mu         sync.Mutex
	calls      []string
	blockUntil <-chan struct{}
}

func (f *fakeIPFSMirrorEnsurer) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeIPFSMirrorEnsurer) wait() {
	if f.blockUntil == nil {
		return
	}
	<-f.blockUntil
}

func (f *fakeIPFSMirrorEnsurer) EnsureLocal(ctx context.Context, c cid.Cid) error {
	f.record("EnsureLocal:" + c.String())
	f.wait()
	return nil
}

func (f *fakeIPFSMirrorEnsurer) EnsureLocalFileOnly(ctx context.Context, c cid.Cid) error {
	f.record("EnsureLocalFileOnly:" + c.String())
	f.wait()
	return nil
}

func (f *fakeIPFSMirrorEnsurer) EnsureLocalWithMirror(ctx context.Context, c cid.Cid, mirrorURL string) error {
	f.record("EnsureLocalWithMirror:" + c.String() + ":" + mirrorURL)
	f.wait()
	return nil
}

func (f *fakeIPFSMirrorEnsurer) EnsureLocalFileOnlyWithMirror(ctx context.Context, c cid.Cid, mirrorURL string) error {
	f.record("EnsureLocalFileOnlyWithMirror:" + c.String() + ":" + mirrorURL)
	f.wait()
	return nil
}

func TestAsyncEnsureIPFSContentReturnsImmediately(t *testing.T) {
	target, err := cid.Decode("bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku")
	if err != nil {
		t.Fatalf("decode cid: %v", err)
	}

	done := make(chan struct{})
	ensurer := &fakeIPFSMirrorEnsurer{blockUntil: done}

	start := time.Now()
	asyncEnsureIPFSContent(context.Background(), time.Second, ensurer, target, false, "https://mirror.example.com")
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("asyncEnsureIPFSContent blocked for %v", elapsed)
	}

	deadline := time.After(200 * time.Millisecond)
	for {
		ensurer.mu.Lock()
		count := len(ensurer.calls)
		ensurer.mu.Unlock()
		if count == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("asyncEnsureIPFSContent did not invoke ensurer")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	close(done)

	ensurer.mu.Lock()
	defer ensurer.mu.Unlock()
	if got := ensurer.calls[0]; got != "EnsureLocalWithMirror:"+target.String()+":https://mirror.example.com" {
		t.Fatalf("unexpected call %q", got)
	}
}

func TestAsyncEnsureIPFSContentUsesFileOnlyPath(t *testing.T) {
	target, err := cid.Decode("bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku")
	if err != nil {
		t.Fatalf("decode cid: %v", err)
	}

	ensurer := &fakeIPFSMirrorEnsurer{}
	asyncEnsureIPFSContent(context.Background(), time.Second, ensurer, target, true, "")

	deadline := time.After(200 * time.Millisecond)
	for {
		ensurer.mu.Lock()
		count := len(ensurer.calls)
		ensurer.mu.Unlock()
		if count == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("asyncEnsureIPFSContent did not invoke ensurer")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	ensurer.mu.Lock()
	defer ensurer.mu.Unlock()
	if got := ensurer.calls[0]; got != "EnsureLocalFileOnly:"+target.String() {
		t.Fatalf("unexpected call %q", got)
	}
}

func TestAsyncEnsureIPFSContentIgnoresParentCancellation(t *testing.T) {
	target, err := cid.Decode("bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku")
	if err != nil {
		t.Fatalf("decode cid: %v", err)
	}

	done := make(chan struct{})
	parent, cancel := context.WithCancel(context.Background())
	cancel()

	ensurer := &fakeIPFSMirrorEnsurer{blockUntil: done}
	asyncEnsureIPFSContent(parent, time.Second, ensurer, target, false, "")

	deadline := time.After(200 * time.Millisecond)
	for {
		ensurer.mu.Lock()
		count := len(ensurer.calls)
		ensurer.mu.Unlock()
		if count == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("asyncEnsureIPFSContent should continue after parent cancellation")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	close(done)

	ensurer.mu.Lock()
	defer ensurer.mu.Unlock()
	if got := ensurer.calls[0]; got != "EnsureLocal:"+target.String() {
		t.Fatalf("unexpected call %q", got)
	}
}
