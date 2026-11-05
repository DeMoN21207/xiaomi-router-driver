package status

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"xiomi-router-driver/internal/config"
)

func TestDNSObserverAppliesConfigurationAndRecoversMissingLog(t *testing.T) {
	dir := t.TempDir()
	configPath, logPath := filepath.Join(dir, "observer.conf"), filepath.Join(dir, "observer.log")
	starts := 0
	restart := func() error {
		starts++
		if _, err := os.ReadFile(configPath); err != nil {
			return err
		}
		return os.WriteFile(logPath, nil, 0600)
	}
	if err := syncDNSObserverConfig(configPath, logPath, restart); err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatalf("new observer configuration was not activated: starts=%d", starts)
	}
	if err := syncDNSObserverConfig(configPath, logPath, restart); err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatal("unchanged observer restarted DNS")
	}
	if err := os.Remove(logPath); err != nil {
		t.Fatal(err)
	}
	if err := syncDNSObserverConfig(configPath, logPath, restart); err != nil {
		t.Fatal(err)
	}
	if starts != 2 {
		t.Fatal("missing observer log was not repaired")
	}
}

func TestDNSObserverRestoresPreviousConfigAndRetriesActivationFailure(t *testing.T) {
	dir := t.TempDir()
	configPath, logPath := filepath.Join(dir, "observer.conf"), filepath.Join(dir, "observer.log")
	if err := os.WriteFile(configPath, []byte("log-queries\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err := syncDNSObserverConfig(configPath, logPath, func() error { return errors.New("restart failed") })
	if err == nil {
		t.Fatal("failed activation was silently accepted")
	}
	previous, _ := os.ReadFile(configPath)
	if string(previous) != "log-queries\n" {
		t.Fatal("failed activation overwrote the previous configuration")
	}
	if err := syncDNSObserverConfig(configPath, logPath, func() error { return os.WriteFile(logPath, nil, 0600) }); err != nil {
		t.Fatal(err)
	}
}

func TestDNSObserverUsesServiceRestartToReadNewInclude(t *testing.T) {
	script := filepath.Join(t.TempDir(), "dnsmasq")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n[ \"$1\" = restart ] || exit 17\nprintf applied > \"$0.applied\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := restartDNSObserver(script); err != nil {
		t.Fatalf("configuration was not applied with restart: %v", err)
	}
	if applied, err := os.ReadFile(script + ".applied"); err != nil || string(applied) != "applied" {
		t.Fatal("DNS service did not apply the new configuration")
	}
}

func TestDNSObserverRetriesServiceAfterFailedMissingLogRepair(t *testing.T) {
	dir := t.TempDir()
	configPath, logPath := filepath.Join(dir, "observer.conf"), filepath.Join(dir, "observer.log")
	start := func() error { return os.WriteFile(logPath, nil, 0600) }
	if err := syncDNSObserverConfig(configPath, logPath, start); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(logPath); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	err := syncDNSObserverConfig(configPath, logPath, func() error {
		attempts++
		if attempts == 1 {
			return errors.New("DNS stopped before service start failed")
		}
		return start()
	})
	if err == nil {
		t.Fatal("failed activation was hidden")
	}
	if _, err := os.Stat(logPath); err != nil || attempts != 2 {
		t.Fatal("DNS was not recovered after failed missing-log repair")
	}
}

func TestTrafficStateMatchesSharedListAndCurrentRuntimeRules(t *testing.T) {
	state := config.State{
		Providers:     []config.Provider{{ID: "vpn", Name: "VPN", Type: config.ProviderTypeSubscription, Enabled: true}},
		Rules:         []config.Rule{{ID: "main", ProviderID: "vpn", SelectedLocation: "Latvia", Enabled: true}},
		SharedDomains: config.SharedDomainList{Entries: []string{"youtube.com"}, RuleIDs: []string{"main"}},
	}
	s := &Service{}
	matcher := buildRouteMatcher(s.trafficState(state))
	if tunneled, _ := matcher.resolve("youtube.com"); !tunneled {
		t.Fatal("shared-list traffic was marked direct")
	}
	s.trafficRules = func(config.State) []config.Rule {
		return []config.Rule{{ID: "runtime", ProviderID: "vpn", SelectedLocation: "Sweden", Domains: []string{"youtube.com"}, Enabled: true}}
	}
	matcher = buildRouteMatcher(s.trafficState(state))
	if tunneled, label := matcher.resolve("youtube.com"); !tunneled || label != "VPN / Sweden" {
		t.Fatalf("runtime selection ignored: %v %q", tunneled, label)
	}
	if len(state.Rules[0].Domains) != 0 || state.Rules[0].SelectedLocation != "Latvia" {
		t.Fatal("saved rules changed")
	}
}
