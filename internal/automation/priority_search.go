package automation

import (
	"context"
	"sync"

	"xiomi-router-driver/internal/config"
)

// All policies share the same two slots, including probes still warming a
// reserve after the policy that started them has returned.
const standbyProbeLimit = 2

type standbyProbePool struct {
	mu      sync.Mutex
	running map[string]*standbyFlight
	changed chan struct{}
}

type standbyFlight struct {
	done     chan struct{}
	probe    providerProbeResult
	canceled bool
}

func (p *standbyProbePool) changes() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.changed == nil {
		p.changed = make(chan struct{})
	}
	return p.changed
}

// Start or join a physical check. A full pool leaves speculative work
// unqueued. A decision that needs this candidate can retry on pool change.
func (s *Supervisor) tryStandby(ctx context.Context, key string, check func() providerProbeResult) *standbyFlight {
	pool := &s.standbyPool
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if ctx.Err() != nil {
		return nil
	}
	if flight := pool.running[key]; flight != nil {
		return flight
	}
	if result, ok := s.cachedProviderProbe(key, standbyProbeCacheAge); ok {
		flight := &standbyFlight{done: make(chan struct{}), probe: result}
		close(flight.done)
		return flight
	}
	if len(pool.running) >= standbyProbeLimit {
		return nil
	}
	if pool.running == nil {
		pool.running = make(map[string]*standbyFlight)
	}
	flight := &standbyFlight{done: make(chan struct{})}
	pool.running[key] = flight
	go func() {
		result := check()
		pool.mu.Lock()
		defer pool.mu.Unlock()
		flight.canceled = ctx.Err() != nil
		flight.probe = result
		if !flight.canceled {
			s.cacheProviderProbe(key, result)
		}
		delete(pool.running, key)
		close(flight.done)
		if pool.changed != nil {
			close(pool.changed)
			pool.changed = nil
		}
	}()
	return flight
}

type priorityProbeSample struct {
	probe  providerProbeResult
	health providerHealthState
}

type prioritySearch struct {
	supervisor *Supervisor
	provider   config.Provider
	check      func(string) providerProbeResult
	record     func(string, providerProbeResult) providerHealthState
	flights    map[string]*standbyFlight
	samples    map[string]priorityProbeSample
}

func (search *prioritySearch) start(ctx context.Context, location string) *standbyFlight {
	if flight := search.flights[location]; flight != nil {
		return flight
	}
	key := standbyProbeKey(search.provider, location)
	flight := search.supervisor.tryStandby(ctx, key, func() providerProbeResult {
		return search.check(location)
	})
	if flight != nil {
		search.flights[location] = flight
	}
	return flight
}

// Only the evaluator publishes policy health. Background probes leave their
// physical result in the cache for the next evaluation.
func (search *prioritySearch) collect(location string) (priorityProbeSample, bool) {
	if sample, ok := search.samples[location]; ok {
		return sample, true
	}
	flight := search.flights[location]
	if flight == nil {
		return priorityProbeSample{}, false
	}
	select {
	case <-flight.done:
		delete(search.flights, location)
		if !flight.canceled {
			sample := priorityProbeSample{probe: flight.probe, health: search.record(location, flight.probe)}
			search.samples[location] = sample
			return sample, true
		}
	default:
	}
	return priorityProbeSample{}, false
}

func (search *prioritySearch) request(ctx context.Context, location string) (priorityProbeSample, bool) {
	if sample, ok := search.collect(location); ok {
		return sample, true
	}
	search.start(ctx, location)
	return search.collect(location)
}

// One slot checks the required candidate. The other advances past failures,
// stopping at the first healthy or still-pending lower-priority candidate.
func (search *prioritySearch) await(ctx context.Context, candidates []string, active string, lookAhead bool) (priorityProbeSample, bool) {
	wanted := candidates[0]
	next := 1
	for ctx.Err() == nil {
		changed := search.supervisor.standbyPool.changes()
		if sample, ok := search.request(ctx, wanted); ok {
			return sample, true
		}
		for lookAhead && search.flights[wanted] != nil && next < len(candidates) {
			location := candidates[next]
			if location != active {
				sample, ok := search.request(ctx, location)
				if !ok || sample.probe.Healthy {
					break
				}
			}
			next++
		}
		select {
		case <-ctx.Done():
			return priorityProbeSample{}, false
		case <-changed:
		}
	}
	return priorityProbeSample{}, false
}
