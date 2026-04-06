package config

import "testing"

func TestIPFSNormalizeDefaultsHTTPMirrorGateway(t *testing.T) {
	cfg := Default()
	cfg.IPFS.HTTPMirrorGateway = ""

	if err := cfg.Normalize(); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if cfg.IPFS.HTTPMirrorGateway != defaultIPFSHTTPMirrorGateway {
		t.Fatalf("HTTPMirrorGateway = %q, want %q", cfg.IPFS.HTTPMirrorGateway, defaultIPFSHTTPMirrorGateway)
	}
}
