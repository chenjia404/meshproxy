package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExportIdentityPrivateKeyBase58(t *testing.T) {
	t.Parallel()

	api := NewLocalAPI(":0", nil, nil, nil, &LocalAPIOpts{
		Identity: stubIdentityProvider{
			exportedPrivateKeyBase58: "3mJr7AoUXx2Wqd",
			exportedPeerID:           "12D3KooWExportPeer",
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/identity/private-key/export", nil)
	rec := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["private_key_base58"] != "3mJr7AoUXx2Wqd" {
		t.Fatalf("want exported private key, got %#v", body["private_key_base58"])
	}
	if body["peer_id"] != "12D3KooWExportPeer" {
		t.Fatalf("want exported peer id, got %#v", body["peer_id"])
	}
	if body["requires_restart"] != false {
		t.Fatalf("want requires_restart=false, got %#v", body["requires_restart"])
	}
}

func TestImportIdentityPrivateKeyBase58(t *testing.T) {
	t.Parallel()

	api := NewLocalAPI(":0", nil, nil, nil, &LocalAPIOpts{
		Identity: stubIdentityProvider{
			importedPeerID: "12D3KooWImportedPeer",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/identity/private-key/import", bytes.NewBufferString(`{"private_key_base58":"3mJr7AoUXx2Wqd"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["peer_id"] != "12D3KooWImportedPeer" {
		t.Fatalf("want imported peer id, got %#v", body["peer_id"])
	}
	if body["requires_restart"] != true {
		t.Fatalf("want requires_restart=true, got %#v", body["requires_restart"])
	}
}

type stubIdentityProvider struct {
	exportedPrivateKeyBase58 string
	exportedPeerID           string
	importedPeerID           string
}

func (s stubIdentityProvider) ExportIdentityPrivateKeyBase58() (string, string, error) {
	return s.exportedPrivateKeyBase58, s.exportedPeerID, nil
}

func (s stubIdentityProvider) ImportIdentityPrivateKeyBase58(encoded string) (string, error) {
	return s.importedPeerID, nil
}
