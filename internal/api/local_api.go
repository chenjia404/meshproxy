package api

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"time"

	"meshproxy/internal/discovery"
	"meshproxy/internal/protocol"
)

//go:embed console/*
var consoleFS embed.FS

// StatusProvider defines the subset of application state required by the local API.
type StatusProvider interface {
	PeerID() string
	Mode() string
	Socks5Listen() string
	P2PListenAddrs() []string
	StartTime() time.Time
}

// NodeProvider provides read access to known nodes.
type NodeProvider interface {
	GetAll() []*discovery.NodeDescriptor
}

// CircuitProvider provides read access to circuits.
type CircuitProvider interface {
	ListCircuits() []protocol.CircuitInfo
}

// RelaysProvider provides list of known relay nodes.
type RelaysProvider interface {
	ListRelays() []*discovery.NodeDescriptor
}

// ExitsProvider provides list of known exit nodes.
type ExitsProvider interface {
	ListExits() []*discovery.NodeDescriptor
}

// StreamsProvider provides list of active streams (for API, use client.StreamInfo).
type StreamsProvider interface {
	ListStreams() []StreamInfoResponse
}

// StreamInfoResponse is the API view of a stream (avoids importing client in api).
type StreamInfoResponse struct {
	ID          string `json:"id"`
	CircuitID   string `json:"circuit_id"`
	TargetHost  string `json:"target_host"`
	TargetPort  int    `json:"target_port"`
	State       string `json:"state"`
	RelayPeerID string `json:"relay_peer_id,omitempty"`
	ExitPeerID  string `json:"exit_peer_id,omitempty"`
	HopCount    int    `json:"hop_count,omitempty"`
}

// CircuitInfoResponse enriches protocol.CircuitInfo with derived fields for API.
type CircuitInfoResponse struct {
	ID          string               `json:"id"`
	State       protocol.CircuitState `json:"state"`
	Plan        protocol.PathPlan    `json:"plan"`
	RelayPeerID string               `json:"relay_peer_id,omitempty"`
	ExitPeerID  string               `json:"exit_peer_id,omitempty"`
	HopCount    int                  `json:"hop_count"`
	StreamCount int                  `json:"stream_count"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

// PoolKindStatusResponse is one pool kind's status for API.
type PoolKindStatusResponse struct {
	IdleCount  int `json:"idle_count"`
	InUseCount int `json:"in_use_count"`
	TotalCount int `json:"total_count"`
}

// PoolStatusResponse is the API view of circuit pool status.
type PoolStatusResponse struct {
	Kinds map[string]PoolKindStatusResponse `json:"kinds"`
}

// ScoresProvider provides peer scores (placeholder for future).
type ScoresProvider interface {
	GetScores() any
}

// RecentErrorsProvider provides recent errors for observability.
type RecentErrorsProvider interface {
	GetRecent() []ErrorEntry
}

// MetricsSummaryProvider provides aggregated metrics.
type MetricsSummaryProvider interface {
	GetSummary() map[string]any
}

// LocalAPIOpts holds optional providers for extended API endpoints.
type LocalAPIOpts struct {
	Relays   RelaysProvider
	Exits    ExitsProvider
	Streams  StreamsProvider
	Pool     PoolStatusProvider
	Scores   ScoresProvider
	Errors   RecentErrorsProvider
	Metrics  MetricsSummaryProvider
}

// PoolStatusProvider returns current circuit pool status.
type PoolStatusProvider interface {
	GetPoolStatus() *PoolStatusResponse
}

// LocalAPI exposes HTTP API for node status, nodes, circuits, relays, exits, streams, pool, scores, errors, metrics.
type LocalAPI struct {
	statusProvider   StatusProvider
	nodeProvider     NodeProvider
	circuitProvider  CircuitProvider
	opts             *LocalAPIOpts
	server           *http.Server
	consoleHTML      []byte // embedded console index.html, served directly to avoid redirect
}

// NewLocalAPI creates a new LocalAPI instance. opts may be nil for minimal API.
func NewLocalAPI(listen string, sp StatusProvider, np NodeProvider, cp CircuitProvider, opts *LocalAPIOpts) *LocalAPI {
	mux := http.NewServeMux()
	api := &LocalAPI{
		statusProvider:  sp,
		nodeProvider:    np,
		circuitProvider: cp,
		opts:            opts,
	}
	mux.HandleFunc("/api/v1/status", api.handleStatus)
	mux.HandleFunc("/api/v1/nodes", api.handleNodes)
	mux.HandleFunc("/api/v1/relays", api.handleRelays)
	mux.HandleFunc("/api/v1/exits", api.handleExits)
	mux.HandleFunc("/api/v1/circuits", api.handleCircuits)
	mux.HandleFunc("/api/v1/streams", api.handleStreams)
	mux.HandleFunc("/api/v1/scores", api.handleScores)
	mux.HandleFunc("/api/v1/errors/recent", api.handleErrorsRecent)
	mux.HandleFunc("/api/v1/metrics/summary", api.handleMetricsSummary)

	// Console: serve index.html directly from embed (no FileServer, no redirect)
	var consoleHTML []byte
	if sub, err := fs.Sub(consoleFS, "console"); err == nil {
		consoleHTML, _ = fs.ReadFile(sub, "index.html")
	}
	api.consoleHTML = consoleHTML

	// Wrap mux to serve /console and /console/ with 200 + body (never redirect)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/console" || path == "/console/" {
			if len(api.consoleHTML) > 0 {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				w.Write(api.consoleHTML)
				return
			}
			http.NotFound(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	api.server = &http.Server{
		Addr:    listen,
		Handler: handler,
	}
	return api
}

// Start launches the HTTP server in a separate goroutine.
func (a *LocalAPI) Start() {
	go func() {
		log.Printf("[api] listening on %s", a.server.Addr)
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[api] server error: %v", err)
		}
	}()
}

// Shutdown gracefully stops the HTTP server.
func (a *LocalAPI) Shutdown() error {
	if a.server == nil {
		return nil
	}
	return a.server.Close()
}

func (a *LocalAPI) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	p := a.statusProvider
	resp := map[string]any{
		"peer_id":          p.PeerID(),
		"mode":             p.Mode(),
		"socks5_listen":    p.Socks5Listen(),
		"p2p_listen_addrs": p.P2PListenAddrs(),
		"uptime_seconds":   int64(time.Since(p.StartTime()).Seconds()),
	}
	if a.opts != nil && a.opts.Relays != nil {
		resp["relays_known"] = len(a.opts.Relays.ListRelays())
	}
	if a.opts != nil && a.opts.Exits != nil {
		resp["exits_known"] = len(a.opts.Exits.ListExits())
	}
	if a.opts != nil && a.opts.Pool != nil {
		if ps := a.opts.Pool.GetPoolStatus(); ps != nil {
			resp["circuit_pool"] = ps
		}
	}
	writeJSON(w, resp)
}

func (a *LocalAPI) handleNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.nodeProvider == nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, a.nodeProvider.GetAll())
}

func (a *LocalAPI) handleCircuits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.circuitProvider == nil {
		writeJSON(w, []any{})
		return
	}
	// Optional: enrich circuit info with hop/stream metadata without exposing any keys.
	circuits := a.circuitProvider.ListCircuits()
	streamCounts := map[string]int{}
	if a.opts != nil && a.opts.Streams != nil {
		streams := a.opts.Streams.ListStreams()
		for _, s := range streams {
			streamCounts[s.CircuitID]++
		}
	}
	out := make([]CircuitInfoResponse, 0, len(circuits))
	for _, c := range circuits {
		hopCount := len(c.Plan.Hops)
		var relayPeerID, exitPeerID string
		if hopCount > 0 && c.Plan.Hops[0].IsRelay {
			relayPeerID = c.Plan.Hops[0].PeerID
		}
		if c.Plan.ExitHopIndex >= 0 && c.Plan.ExitHopIndex < hopCount {
			exitHop := c.Plan.Hops[c.Plan.ExitHopIndex]
			if exitHop.IsExit {
				exitPeerID = exitHop.PeerID
			}
		}
		out = append(out, CircuitInfoResponse{
			ID:          c.ID,
			State:       c.State,
			Plan:        c.Plan,
			RelayPeerID: relayPeerID,
			ExitPeerID:  exitPeerID,
			HopCount:    hopCount,
			StreamCount: streamCounts[c.ID],
			CreatedAt:   c.CreatedAt,
			UpdatedAt:   c.UpdatedAt,
		})
	}
	writeJSON(w, out)
}

func (a *LocalAPI) handleRelays(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.Relays == nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, a.opts.Relays.ListRelays())
}

func (a *LocalAPI) handleExits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.Exits == nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, a.opts.Exits.ListExits())
}

func (a *LocalAPI) handleStreams(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.Streams == nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, a.opts.Streams.ListStreams())
}

func (a *LocalAPI) handleScores(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.Scores == nil {
		writeJSON(w, map[string]any{"peers": []any{}})
		return
	}
	writeJSON(w, a.opts.Scores.GetScores())
}

func (a *LocalAPI) handleErrorsRecent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.Errors == nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, a.opts.Errors.GetRecent())
}

func (a *LocalAPI) handleMetricsSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.opts == nil || a.opts.Metrics == nil {
		writeJSON(w, map[string]any{})
		return
	}
	writeJSON(w, a.opts.Metrics.GetSummary())
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "encode response error", http.StatusInternalServerError)
		return
	}
}


