package api

import (
	"net/http/httptest"
	"testing"
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
