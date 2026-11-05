package automation

import (
	"context"
	"sync"
)

type priorityProbeCycleKey struct{}

type priorityProbeCycle struct {
	mu     sync.Mutex
	active map[string]*priorityCycleProbe
}

type priorityCycleProbe struct {
	done   chan struct{}
	result providerProbeResult
}

// Share a physical tunnel check between policies within this evaluation only.
// Results are not retained across evaluations; normal tunnel cache limits apply.
func (s *Supervisor) priorityTunnelProbe(ctx context.Context, iface, mark string) providerProbeResult {
	cycle, ok := ctx.Value(priorityProbeCycleKey{}).(*priorityProbeCycle)
	if !ok {
		return s.subscriptionTunnelProbe(ctx, iface, mark)
	}
	key := iface + ":" + mark
	cycle.mu.Lock()
	if pending, exists := cycle.active[key]; exists {
		cycle.mu.Unlock()
		select {
		case <-pending.done:
			return pending.result
		case <-ctx.Done():
			return providerProbeResult{Detail: "shared tunnel check cancelled"}
		}
	}
	pending := &priorityCycleProbe{done: make(chan struct{})}
	cycle.active[key] = pending
	cycle.mu.Unlock()
	pending.result = s.subscriptionTunnelProbe(ctx, iface, mark)
	close(pending.done)
	return pending.result
}
