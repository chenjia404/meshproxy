package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/chenjia404/meshproxy/internal/ipfsnode"
	"github.com/chenjia404/meshproxy/internal/ipfspin"

	cid "github.com/ipfs/go-cid"
)

type ipfsJSONError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeIPFSError(w http.ResponseWriter, code, msg string, status int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	var e ipfsJSONError
	e.Error.Code = code
	e.Error.Message = msg
	_ = json.NewEncoder(w).Encode(e)
}

func (a *LocalAPI) handleIPFSAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeIPFSError(w, "BAD_REQUEST", "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.IPFS == nil || !a.opts.IPFS.APIEnabled() {
		http.NotFound(w, r)
		return
	}
	maxB := a.opts.IPFS.MaxUploadBytes()
	r.Body = http.MaxBytesReader(w, r.Body, maxB+1)
	if err := r.ParseMultipartForm(maxB + 1); err != nil {
		var me *http.MaxBytesError
		if errors.As(err, &me) {
			writeIPFSError(w, "PAYLOAD_TOO_LARGE", "upload exceeds limit", http.StatusRequestEntityTooLarge)
			return
		}
		writeIPFSError(w, "BAD_REQUEST", "invalid multipart form", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeIPFSError(w, "BAD_REQUEST", "missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	pin := a.opts.IPFS.AutoPinOnAdd()
	if v := r.FormValue("pin"); v != "" {
		pin, _ = strconv.ParseBool(v)
	}
	rawLeaves := true
	if v := r.FormValue("rawLeaves"); v != "" {
		rawLeaves, _ = strconv.ParseBool(v)
	}
	cidVer := 1
	if v := r.FormValue("cidVersion"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cidVer = n
		}
	}
	if cidVer != 1 {
		writeIPFSError(w, "BAD_REQUEST", "only cidVersion=1 supported", http.StatusBadRequest)
		return
	}

	opt := ipfsnode.AddFileOptions{
		Filename:   r.FormValue("filename"),
		RawLeaves:  rawLeaves,
		CIDVersion: cidVer,
		Chunker:    "size-262144",
		Pin:        pin,
	}
	svc := a.opts.IPFS.Service()
	c, err := svc.AddFile(r.Context(), file, opt)
	if err != nil {
		log.Printf("[ipfs] module=ipfs op=add error=%v", err)
		writeIPFSError(w, "INTERNAL_ERROR", "add failed", http.StatusInternalServerError)
		return
	}
	st, err := svc.Stat(r.Context(), c)
	if err != nil {
		log.Printf("[ipfs] module=ipfs op=add-stat error=%v", err)
	}
	size := int64(0)
	pinned := pin
	if st != nil {
		size = st.Size
		pinned = st.Pinned
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"cid":    c.String(),
		"size":   size,
		"pinned": pinned,
	})
}

func (a *LocalAPI) handleIPFSAddDir(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.IPFS == nil || !a.opts.IPFS.APIEnabled() {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		writeIPFSError(w, "BAD_REQUEST", "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "not implemented"})
}

func (a *LocalAPI) handleIPFSStat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeIPFSError(w, "BAD_REQUEST", "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.IPFS == nil || !a.opts.IPFS.APIEnabled() {
		http.NotFound(w, r)
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, "/api/ipfs/stat/")
	suffix = strings.Trim(suffix, "/")
	if suffix == "" {
		writeIPFSError(w, "BAD_REQUEST", "missing cid", http.StatusBadRequest)
		return
	}
	c, err := cid.Decode(suffix)
	if err != nil {
		writeIPFSError(w, "INVALID_CID", "invalid cid", http.StatusBadRequest)
		return
	}
	st, err := a.opts.IPFS.Service().Stat(r.Context(), c)
	if err != nil {
		writeIPFSError(w, "CID_NOT_FOUND", "content not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"cid":      st.CID,
		"size":     st.Size,
		"numLinks": st.NumLinks,
		"local":    st.Local,
		"pinned":   st.Pinned,
	})
}

type pinBody struct {
	Recursive bool `json:"recursive"`
}

func (a *LocalAPI) handleIPFSPin(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.IPFS == nil || !a.opts.IPFS.APIEnabled() {
		http.NotFound(w, r)
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, "/api/ipfs/pin/")
	suffix = strings.Trim(suffix, "/")
	if suffix == "" {
		writeIPFSError(w, "BAD_REQUEST", "missing cid", http.StatusBadRequest)
		return
	}
	c, err := cid.Decode(suffix)
	if err != nil {
		writeIPFSError(w, "INVALID_CID", "invalid cid", http.StatusBadRequest)
		return
	}
	svc := a.opts.IPFS.Service()
	switch r.Method {
	case http.MethodPost:
		recursive := true
		if r.ContentLength > 0 {
			var b pinBody
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&b); err == nil {
				recursive = b.Recursive
			}
		}
		if err := svc.Pin(r.Context(), c, recursive); err != nil {
			if errors.Is(err, ipfspin.ErrNotImplemented) {
				writeIPFSError(w, "NOT_IMPLEMENTED", err.Error(), http.StatusNotImplemented)
				return
			}
			writeIPFSError(w, "PIN_FAILED", err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "cid": c.String()})
	case http.MethodDelete:
		recursive := true
		if err := svc.Unpin(r.Context(), c, recursive); err != nil {
			if errors.Is(err, ipfspin.ErrNotImplemented) {
				writeIPFSError(w, "NOT_IMPLEMENTED", err.Error(), http.StatusNotImplemented)
				return
			}
			writeIPFSError(w, "UNPIN_FAILED", err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "cid": c.String()})
	default:
		writeIPFSError(w, "BAD_REQUEST", "method not allowed", http.StatusMethodNotAllowed)
	}
}
