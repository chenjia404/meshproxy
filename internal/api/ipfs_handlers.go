package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
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

func ipfsMirrorURLFromHeader(r *http.Request) (string, error) {
	raw := strings.TrimSpace(r.Header.Get("http_mirror_gateway"))
	if raw == "" {
		return "", nil
	}
	if strings.ContainsAny(raw, "/?#") {
		return "", errors.New("http_mirror_gateway must be a host only")
	}
	if strings.Contains(raw, "://") {
		return "", errors.New("http_mirror_gateway must not include scheme")
	}
	if strings.ContainsAny(raw, " \t\r\n") {
		return "", errors.New("http_mirror_gateway must not include whitespace")
	}
	u, err := url.Parse("https://" + raw)
	if err != nil {
		return "", errors.New("invalid http_mirror_gateway host")
	}
	if strings.TrimSpace(u.Host) == "" || u.Host != raw {
		return "", errors.New("invalid http_mirror_gateway host")
	}
	return "https://" + raw, nil
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
	a.execIPFSAdd(w, r)
}

// handleIPFSGatewayUpload 處理 POST/PUT /ipfs 與 /ipfs/（需 gateway_writable）。
func (a *LocalAPI) handleIPFSGatewayUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeIPFSError(w, "BAD_REQUEST", "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.IPFS == nil || !a.opts.IPFS.GatewayWritable() {
		http.NotFound(w, r)
		return
	}
	a.execIPFSAdd(w, r)
}

// execIPFSAdd multipart 欄位 file，行為同 /api/ipfs/add。
func (a *LocalAPI) execIPFSAdd(w http.ResponseWriter, r *http.Request) {
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
	rawLeaves := a.opts.IPFS.RawLeaves()
	if v := r.FormValue("rawLeaves"); v != "" {
		rawLeaves, _ = strconv.ParseBool(v)
	}
	hashFunction := a.opts.IPFS.HashFunction()
	if v := r.FormValue("hashFunction"); v != "" {
		hashFunction = v
	}
	chunker := a.opts.IPFS.Chunker()
	if v := r.FormValue("chunker"); v != "" {
		chunker = v
	}
	cidVer := a.opts.IPFS.CIDVersion()
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
		Filename:     r.FormValue("filename"),
		RawLeaves:    rawLeaves,
		CIDVersion:   cidVer,
		HashFunction: hashFunction,
		Chunker:      chunker,
		Pin:          pin,
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

// serveIPFSGateway 閘道讀取 + 可選 POST/PUT /ipfs/ 寫入。
func (a *LocalAPI) serveIPFSGateway(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.IPFS == nil {
		http.NotFound(w, r)
		return
	}
	gw := a.opts.IPFS.GatewayHandler()
	p := r.URL.Path
	if a.opts.IPFS.GatewayWritable() && (p == "/ipfs" || p == "/ipfs/") {
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			a.handleIPFSGatewayUpload(w, r)
			return
		}
	}
	if strings.HasPrefix(p, "/ipfs/") && p != "/ipfs/" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeIPFSError(w, "BAD_REQUEST", "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		suffix := strings.TrimPrefix(p, "/ipfs/")
		rootCID := suffix
		if idx := strings.IndexByte(rootCID, '/'); idx >= 0 {
			rootCID = rootCID[:idx]
		}
		rootCID = strings.TrimSpace(rootCID)
		if rootCID != "" {
			if c, err := cid.Decode(rootCID); err == nil {
				mirrorURL, mirrorErr := ipfsMirrorURLFromHeader(r)
				if mirrorErr != nil {
					writeIPFSError(w, "BAD_REQUEST", mirrorErr.Error(), http.StatusBadRequest)
					return
				}
				fileOnly := strings.TrimSpace(r.URL.Query().Get("filename")) != ""
				if !fileOnly {
					remainder := strings.TrimPrefix(suffix, rootCID)
					remainder = strings.TrimSpace(remainder)
					fileOnly = remainder != ""
				}
				var ensureErr error
				if fileOnly {
					if mirrorURL != "" {
						ensureErr = a.opts.IPFS.EnsureLocalFileOnlyWithMirror(r.Context(), c, mirrorURL)
					} else {
						ensureErr = a.opts.IPFS.EnsureLocalFileOnly(r.Context(), c)
					}
				} else {
					if mirrorURL != "" {
						ensureErr = a.opts.IPFS.EnsureLocalWithMirror(r.Context(), c, mirrorURL)
					} else {
						ensureErr = a.opts.IPFS.EnsureLocal(r.Context(), c)
					}
				}
				if ensureErr != nil {
					log.Printf("[ipfs] module=ipfs op=mirror-fetch cid=%s file_only=%t mirror=%q error=%v", c.String(), fileOnly, mirrorURL, ensureErr)
				}
			}
		}
	}
	gw.ServeHTTP(w, r)
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
