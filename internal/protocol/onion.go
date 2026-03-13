// Package protocol 實現洋蔥分層封裝與解封：forward/backward 方向的多層加密與單層解密。
package protocol

import (
	"encoding/json"
	"fmt"
)

// WrapForward 按路徑從最後一跳向第一跳逐層加密。hops 順序為 [relay, exit]（先加密給 exit，再包一層給 relay）。
// innerPlaintext 為最內層業務負載（如 JSON 編碼的 OnionPayload）。
func WrapForward(hops []*HopSession, circuitID, streamID string, innerPlaintext []byte) (outerCiphertext []byte, err error) {
	if len(hops) == 0 {
		return nil, fmt.Errorf("onion: no hops")
	}
	plaintext := innerPlaintext
	for i := len(hops) - 1; i >= 0; i-- {
		hop := hops[i]
		var toEncrypt []byte
		if i == len(hops)-1 {
			toEncrypt = plaintext
		} else {
			env := OnionEnvelope{
				Header: RelayLayerHeader{
					NextPeerID:       hops[i+1].PeerID,
					InnerPayloadType: InnerPayloadTypeOnion,
					StreamID:         streamID,
				},
				InnerCiphertext: plaintext,
			}
			toEncrypt, err = json.Marshal(env)
			if err != nil {
				return nil, err
			}
		}
		nonce := BuildAEADNonce("fwd", hop.ForwardCounter)
		hop.ForwardCounter++
		aad := BuildAAD(circuitID, streamID, "fwd", uint8(i))
		plaintext, err = AEADSeal(hop.ForwardKey, nonce, toEncrypt, aad)
		if err != nil {
			return nil, err
		}
	}
	return plaintext, nil
}

// UnwrapForward 用當前 hop 的 forward key 解一層，返回本層頭與內層密文。
func UnwrapForward(hop *HopSession, circuitID, streamID string, ciphertext []byte) (header RelayLayerHeader, innerCiphertext []byte, err error) {
	nonce := BuildAEADNonce("fwd", hop.ForwardCounter)
	hop.ForwardCounter++
	aad := BuildAAD(circuitID, streamID, "fwd", 0)
	plain, err := AEADOpen(hop.ForwardKey, nonce, ciphertext, aad)
	if err != nil {
		return header, nil, fmt.Errorf("onion unwrap forward: %w", err)
	}
	var env OnionEnvelope
	if err := json.Unmarshal(plain, &env); err != nil {
		return header, nil, fmt.Errorf("onion unmarshal envelope: %w", err)
	}
	return env.Header, env.InnerCiphertext, nil
}

// UnwrapForwardFinal 由 exit 調用：解最後一層得到業務明文（OnionPayload 的 JSON）。
func UnwrapForwardFinal(hop *HopSession, circuitID, streamID string, ciphertext []byte) (innerPlaintext []byte, err error) {
	nonce := BuildAEADNonce("fwd", hop.ForwardCounter)
	hop.ForwardCounter++
	aad := BuildAAD(circuitID, streamID, "fwd", 0)
	return AEADOpen(hop.ForwardKey, nonce, ciphertext, aad)
}

// WrapBackward 按 backward 方向逐層加密（exit 先封裝，relay 再封一層）。hops 順序為 [relay, exit]。
// 封裝時從最後一跳向第一跳：先 exit.BackwardKey，再 relay.BackwardKey。
func WrapBackward(hops []*HopSession, circuitID, streamID string, innerPlaintext []byte) (outerCiphertext []byte, err error) {
	if len(hops) == 0 {
		return nil, fmt.Errorf("onion: no hops for backward")
	}
	plaintext := innerPlaintext
	for i := len(hops) - 1; i >= 0; i-- {
		hop := hops[i]
		var toEncrypt []byte
		if i == len(hops)-1 {
			toEncrypt = plaintext
		} else {
			env := OnionEnvelope{
				Header: RelayLayerHeader{
					NextPeerID:       hops[i+1].PeerID,
					InnerPayloadType: InnerPayloadTypeOnion,
					StreamID:         streamID,
				},
				InnerCiphertext: plaintext,
			}
			var errEnc error
			toEncrypt, errEnc = json.Marshal(env)
			if errEnc != nil {
				return nil, errEnc
			}
		}
		nonce := BuildAEADNonce("bwd", hop.BackwardCounter)
		hop.BackwardCounter++
		aad := BuildAAD(circuitID, streamID, "bwd", uint8(i))
		plaintext, err = AEADSeal(hop.BackwardKey, nonce, toEncrypt, aad)
		if err != nil {
			return nil, err
		}
	}
	return plaintext, nil
}

// UnwrapBackward 客戶端解一層 backward 密文，得到內層明文或內層密文（繼續解）。
func UnwrapBackward(hop *HopSession, circuitID, streamID string, ciphertext []byte) (innerPlaintext []byte, err error) {
	nonce := BuildAEADNonce("bwd", hop.BackwardCounter)
	hop.BackwardCounter++
	aad := BuildAAD(circuitID, streamID, "bwd", 0)
	return AEADOpen(hop.BackwardKey, nonce, ciphertext, aad)
}
