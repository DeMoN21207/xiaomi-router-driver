package automation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/runtimebin"
	"xiomi-router-driver/internal/subscription"
)

type CandidateProbe struct {
	Location  string `json:"location"`
	Healthy   bool   `json:"healthy"`
	LatencyMs int64  `json:"latencyMs,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// ProbeSubscriptionCandidates checks saved subscription endpoints without
// changing active tunnels or exposing subscription credentials to the caller.
func ProbeSubscriptionCandidates(ctx context.Context, provider config.Provider, dataDir string, names []string) ([]CandidateProbe, error) {
	if !provider.Enabled || provider.Type != config.ProviderTypeSubscription || len(names) == 0 || len(names) > 8 {
		return nil, errors.New("provide 1 to 8 locations of an enabled subscription")
	}
	s := &Supervisor{dataDir: dataDir}
	entries, err := subscription.LoadCachedEntries(provider.Source, s.subscriptionRuntimeDir())
	if err != nil {
		return nil, errors.New("cached subscription is not available; refresh the server catalogue")
	}
	selected := make([]subscription.Entry, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		entry, found := findSubscriptionEntry(entries, name)
		if !found || subscription.EntryEndpointIssue(entry) != "" {
			return nil, errors.New("a location is missing or has an invalid endpoint")
		}
		key := strings.ToLower(entry.Name)
		if !seen[key] {
			seen[key] = true
			selected = append(selected, entry)
		}
	}
	return probeCandidateEntries(ctx, runtimebin.Resolve(os.Getenv("VPN_MANAGER_SINGBOX_BIN"), "sing-box", filepath.Dir(dataDir), dataDir), selected, probeStandbyEntry), nil
}

func probeCandidateEntries(ctx context.Context, binary string, entries []subscription.Entry, check func(context.Context, string, subscription.Entry) providerProbeResult) []CandidateProbe {
	results := make([]CandidateProbe, len(entries))
	slots := make(chan struct{}, 3)
	var workers sync.WaitGroup
	for index, entry := range entries {
		workers.Add(1)
		go func(index int, entry subscription.Entry) {
			defer workers.Done()
			if ctx.Err() != nil {
				results[index] = CandidateProbe{Location: entry.Name, Detail: "probe cancelled"}
				return
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				results[index] = CandidateProbe{Location: entry.Name, Detail: "probe cancelled"}
				return
			}
			defer func() { <-slots }()
			probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			result := check(probeCtx, binary, entry)
			results[index] = CandidateProbe{Location: entry.Name, Healthy: result.Healthy, LatencyMs: result.LatencyMs, Detail: result.Detail}
		}(index, entry)
	}
	workers.Wait()
	return results
}
