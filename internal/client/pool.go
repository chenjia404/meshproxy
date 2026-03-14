package client

import (
	"context"
	"sync"
	"time"

	"github.com/chenjia404/meshproxy/internal/config"
)

// PoolKind is the circuit pool type by use case.
type PoolKind string

const (
	// PoolLowLatency prefers direct exit for minimal latency.
	PoolLowLatency PoolKind = "low_latency"
	// PoolAnonymous prefers relay+exit for better anonymity.
	PoolAnonymous PoolKind = "anonymous"
	// PoolCountry is for country-specific exit (country code set via config; same path as anonymous for now).
	PoolCountry PoolKind = "country"
)

// AllPoolKinds returns all pool kinds that are maintained.
func AllPoolKinds() []PoolKind {
	return []PoolKind{PoolLowLatency, PoolAnonymous, PoolCountry}
}

// poolEntry is an idle circuit in the pool.
type poolEntry struct {
	circuitID string
	idleSince time.Time
}

// CircuitPoolFactory is used by CircuitPool to create and close circuits.
type CircuitPoolFactory interface {
	CreateForPool(kind PoolKind) (circuitID string, err error)
	CloseCircuit(circuitID string)
	IsCircuitOpen(circuitID string) bool
}

// CircuitPool holds pre-built circuits per kind and maintains min/max, idle timeout, replenish.
type CircuitPool struct {
	mu sync.Mutex

	minPerPool   int
	maxPerPool   int
	idleTimeout  time.Duration
	replenishInt time.Duration

	factory CircuitPoolFactory

	// idle[kind] = list of { circuitID, idleSince }
	idle map[PoolKind][]poolEntry
	// circuitToKind so we can return/fail by circuitID
	circuitToKind map[string]PoolKind
	// inUseStreamCount[circuitID] = number of streams on this circuit (0 = not lent, or in idle)
	inUseStreamCount map[string]int
	// outCount[kind] = number of circuits of this kind that have at least one stream (lent)
	outCount map[PoolKind]int
}

// NewCircuitPool creates a circuit pool with the given config and factory.
func NewCircuitPool(cfg config.CircuitPoolConfig, factory CircuitPoolFactory) *CircuitPool {
	return &CircuitPool{
		minPerPool:    cfg.MinPerPool,
		maxPerPool:    cfg.MaxPerPool,
		idleTimeout:   time.Duration(cfg.IdleTimeoutSeconds) * time.Second,
		replenishInt:  time.Duration(cfg.ReplenishIntervalSeconds) * time.Second,
		factory:         factory,
		idle:            make(map[PoolKind][]poolEntry),
		circuitToKind:   make(map[string]PoolKind),
		inUseStreamCount: make(map[string]int),
		outCount:        make(map[PoolKind]int),
	}
}

// GetFromPool returns a circuit for a new stream: prefers reusing an already-in-use circuit
// (multiple streams per circuit) that is still open; if none, takes from idle or returns false.
func (p *CircuitPool) GetFromPool(kind PoolKind) (circuitID string, ok bool) {
	p.mu.Lock()
	for cid, n := range p.inUseStreamCount {
		if n > 0 && p.circuitToKind[cid] == kind {
			p.mu.Unlock()
			if p.factory.IsCircuitOpen(cid) {
				p.mu.Lock()
				if p.inUseStreamCount[cid] > 0 {
					p.inUseStreamCount[cid]++
					cidRet := cid
					p.mu.Unlock()
					return cidRet, true
				}
			} else {
				p.mu.Lock()
			}
			continue
		}
	}
	// Take from idle; skip any that are no longer open
	for len(p.idle[kind]) > 0 {
		list := p.idle[kind]
		entry := list[len(list)-1]
		list = list[:len(list)-1]
		p.idle[kind] = list
		circuitID = entry.circuitID
		p.mu.Unlock()
		if p.factory.IsCircuitOpen(circuitID) {
			p.mu.Lock()
			p.inUseStreamCount[circuitID] = 1
			p.outCount[kind]++
			p.mu.Unlock()
			return circuitID, true
		}
		p.mu.Lock()
		delete(p.circuitToKind, circuitID)
	}
	p.mu.Unlock()
	return "", false
}

// ReturnToPool is called when a stream on this circuit closes. Puts the circuit back to idle
// only when its stream count drops to zero (so multiple streams can share one circuit).
func (p *CircuitPool) ReturnToPool(circuitID string) {
	p.mu.Lock()
	kind, inPool := p.circuitToKind[circuitID]
	if !inPool {
		p.mu.Unlock()
		return
	}
	n, _ := p.inUseStreamCount[circuitID]
	if n <= 0 {
		p.mu.Unlock()
		return
	}
	n--
	p.inUseStreamCount[circuitID] = n
	if n > 0 {
		p.mu.Unlock()
		return
	}
	delete(p.inUseStreamCount, circuitID)
	p.outCount[kind]--
	p.mu.Unlock()

	if !p.factory.IsCircuitOpen(circuitID) {
		p.mu.Lock()
		delete(p.circuitToKind, circuitID)
		p.mu.Unlock()
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.idle[kind] = append(p.idle[kind], poolEntry{circuitID: circuitID, idleSince: time.Now()})
}

// MarkCircuitFailed removes the circuit from pool accounting and does not return it to idle.
// Call when a lent circuit failed so the maintainer can replenish.
func (p *CircuitPool) MarkCircuitFailed(circuitID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	kind, ok := p.circuitToKind[circuitID]
	if !ok {
		return
	}
	delete(p.circuitToKind, circuitID)
	if p.inUseStreamCount[circuitID] > 0 {
		delete(p.inUseStreamCount, circuitID)
		p.outCount[kind]--
	}
}

// registerPoolCircuit is called when we add a newly created circuit to the idle pool.
func (p *CircuitPool) registerPoolCircuit(kind PoolKind, circuitID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.idle[kind] = append(p.idle[kind], poolEntry{circuitID: circuitID, idleSince: time.Now()})
	p.circuitToKind[circuitID] = kind
}

// RegisterCircuitInUse is called when a new circuit is created on demand (not from idle);
// it is lent with one stream and must not be in the idle list.
func (p *CircuitPool) RegisterCircuitInUse(kind PoolKind, circuitID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.circuitToKind[circuitID] = kind
	p.inUseStreamCount[circuitID] = 1
	p.outCount[kind]++
}

// totalCount returns idle count + lent circuit count for a kind.
func (p *CircuitPool) totalCount(kind PoolKind) int {
	return len(p.idle[kind]) + p.outCount[kind]
}

// runMaintenance creates missing circuits, evicts idle circuits that exceeded timeout.
func (p *CircuitPool) runMaintenance(ctx context.Context) {
	ticker := time.NewTicker(p.replenishInt)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.maintainOnce(ctx)
		}
	}
}

func (p *CircuitPool) maintainOnce(ctx context.Context) {
	for _, kind := range AllPoolKinds() {
		p.mu.Lock()
		total := p.totalCount(kind)
		idleList := p.idle[kind]
		toEvict := 0
		now := time.Now()
		for _, e := range idleList {
			if now.Sub(e.idleSince) > p.idleTimeout {
				toEvict++
			}
		}
		p.mu.Unlock()

		// Evict old idle circuits (outside lock when calling factory.CloseCircuit)
		if toEvict > 0 {
			p.mu.Lock()
			list := p.idle[kind]
			evicted := list[len(list)-toEvict:]
			p.idle[kind] = list[:len(list)-toEvict]
			for _, e := range evicted {
				delete(p.circuitToKind, e.circuitID)
			}
			p.mu.Unlock()
			for _, e := range evicted {
				p.factory.CloseCircuit(e.circuitID)
			}
		}

		// Replenish up to min idle
		for {
			p.mu.Lock()
			total = p.totalCount(kind)
			idleLen := len(p.idle[kind])
			need := p.minPerPool - idleLen
			canCreate := total < p.maxPerPool && need > 0
			p.mu.Unlock()

			if !canCreate {
				break
			}
			circuitID, err := p.factory.CreateForPool(kind)
			if err != nil || circuitID == "" {
				break
			}
			p.registerPoolCircuit(kind, circuitID)
		}
	}
}

// StartMaintenance starts the background pool maintainer. Call with app context.
func (p *CircuitPool) StartMaintenance(ctx context.Context) {
	go p.runMaintenance(ctx)
}

// PoolKindStatus is the status of one pool kind for API.
type PoolKindStatus struct {
	IdleCount  int `json:"idle_count"`
	InUseCount int `json:"in_use_count"`
	TotalCount int `json:"total_count"`
}

// PoolStatus is the full circuit pool status for API.
type PoolStatus struct {
	Kinds map[string]PoolKindStatus `json:"kinds"`
}

// Status returns current pool status (idle count and lent circuit count per kind).
func (p *CircuitPool) Status() PoolStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	kinds := make(map[string]PoolKindStatus)
	for _, kind := range AllPoolKinds() {
		idleLen := len(p.idle[kind])
		lentCount := p.outCount[kind]
		kinds[string(kind)] = PoolKindStatus{
			IdleCount:  idleLen,
			InUseCount: lentCount,
			TotalCount: idleLen + lentCount,
		}
	}
	return PoolStatus{Kinds: kinds}
}
