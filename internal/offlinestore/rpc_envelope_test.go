package offlinestore

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestRpcBodyHasOKKey(t *testing.T) {
	t.Parallel()
	if !rpcBodyHasOKKey([]byte(`{"ok":true}`)) {
		t.Fatal("business success must have ok key")
	}
	if !rpcBodyHasOKKey([]byte(`{"ok":false,"error_code":"X"}`)) {
		t.Fatal("business failure must have ok key")
	}
	if rpcBodyHasOKKey([]byte(`{"error_code":"RPC_UNKNOWN_METHOD","error_message":"bad"}`)) {
		t.Fatal("RPCErrorBody must not count as business shape")
	}
	if rpcBodyHasOKKey([]byte(`{}`)) {
		t.Fatal("empty object must be false")
	}
	if rpcBodyHasOKKey(nil) {
		t.Fatal("nil must be false")
	}
}

func TestRpcLayerErrorFromBody(t *testing.T) {
	t.Parallel()
	err := rpcLayerErrorFromBody([]byte(`{"error_code":"E1","error_message":"msg"}`))
	if err == nil || !errors.Is(err, ErrRPCLayer) {
		t.Fatalf("want ErrRPCLayer, got %v", err)
	}
	err = rpcLayerErrorFromBody([]byte(`{"error_code":"E2"}`))
	if err == nil || !errors.Is(err, ErrRPCLayer) {
		t.Fatalf("want ErrRPCLayer, got %v", err)
	}
	if rpcLayerErrorFromBody([]byte(`{}`)) == nil {
		t.Fatal("missing error_code must error")
	}
}

func TestHandleRPCEnvelope(t *testing.T) {
	t.Parallel()
	t.Run("rpc_ok_success", func(t *testing.T) {
		body := []byte(`{"ok":true,"items":[],"has_more":false}`)
		ok, out, err := handleRPCEnvelope(RPCResponse{OK: true, Body: body})
		if err != nil || !ok || string(out) != string(body) {
			t.Fatalf("got ok=%v err=%v out=%s", ok, err, out)
		}
	})
	t.Run("rpc_ok_missing_business_ok", func(t *testing.T) {
		_, _, err := handleRPCEnvelope(RPCResponse{OK: true, Body: []byte(`{"error_code":"X"}`)})
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("rpc_fail_rpc_layer", func(t *testing.T) {
		body := []byte(`{"error_code":"RPC_UNKNOWN_METHOD","error_message":"unknown","method":"x"}`)
		ok, out, err := handleRPCEnvelope(RPCResponse{OK: false, Body: body})
		if err == nil || ok || out != nil {
			t.Fatalf("want rpc layer error, got ok=%v out=%v err=%v", ok, out, err)
		}
		if !errors.Is(err, ErrRPCLayer) {
			t.Fatalf("want ErrRPCLayer: %v", err)
		}
	})
	t.Run("rpc_fail_business", func(t *testing.T) {
		body := []byte(`{"ok":false,"error_code":"STORE_FULL","error_message":"disk"}`)
		ok, out, err := handleRPCEnvelope(RPCResponse{OK: false, Body: body})
		if err != nil || ok || string(out) != string(body) {
			t.Fatalf("got ok=%v err=%v out=%s", ok, err, out)
		}
	})
	t.Run("rpc_fail_empty_body", func(t *testing.T) {
		_, _, err := handleRPCEnvelope(RPCResponse{OK: false, Error: "oops", Body: nil})
		if err == nil || !errors.Is(err, ErrRPCLayer) {
			t.Fatalf("want ErrRPCLayer: %v", err)
		}
	})
}

func TestValidateRPCRequestID(t *testing.T) {
	t.Parallel()
	reqID := "550e8400-e29b-41d4-a716-446655440000"
	if err := validateRPCRequestID(reqID, RPCResponse{RequestID: reqID}); err != nil {
		t.Fatal(err)
	}
	if err := validateRPCRequestID(reqID, RPCResponse{RequestID: "other"}); err == nil {
		t.Fatal("mismatch must fail")
	}
	if !errors.Is(validateRPCRequestID(reqID, RPCResponse{RequestID: "other"}), ErrRPCRequestIDMismatch) {
		t.Fatal("want ErrRPCRequestIDMismatch")
	}
	if err := validateRPCRequestID(reqID, RPCResponse{RequestID: ""}); err == nil {
		t.Fatal("empty response id must fail")
	}
}

func TestHandleRPCEnvelope_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	// 未知 method 等 RPC 层响应：外层 ok=false，body 无 "ok"
	raw := `{"request_id":"r1","ok":false,"error":"unknown method","body":{"error_code":"RPC_UNKNOWN_METHOD","error_message":"x","method":"bad"}}`
	var outer RPCResponse
	if err := json.Unmarshal([]byte(raw), &outer); err != nil {
		t.Fatal(err)
	}
	ok, body, err := handleRPCEnvelope(outer)
	if err == nil || ok || body != nil {
		t.Fatalf("expected RPC layer error, ok=%v body=%v err=%v", ok, body, err)
	}
}
