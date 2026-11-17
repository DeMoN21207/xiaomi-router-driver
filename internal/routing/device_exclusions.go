package routing

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"
	"xiomi-router-driver/internal/config"
)

// SetDeviceExclusionsPath is called once, before any routing workers start.
func (r *Runner) SetDeviceExclusionsPath(path string) { r.deviceExclusionsPath = path }

func (r *Runner) ApplyDeviceExclusions(ctx context.Context, settings config.RoutingSettings, items []config.DeviceExclusion, changedIPs []string) error {
	r.deviceExclusionsMu.Lock()
	defer r.deviceExclusionsMu.Unlock()
	if r.deviceExclusionsPath == "" {
		return errors.New("device exclusions path is not configured")
	}
	var body strings.Builder
	for _, item := range items {
		mac, err := config.NormalizeDeviceMAC(item.MAC)
		if err != nil {
			return err
		}
		body.WriteString(mac + "\n")
	}
	for _, value := range changedIPs {
		ip, err := netip.ParseAddr(value)
		if err != nil || !ip.Is4() || !ip.IsPrivate() {
			return errors.New("invalid device IPv4 address")
		}
	}
	previous, err := os.ReadFile(r.deviceExclusionsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeDeviceList(r.deviceExclusionsPath, []byte(body.String())); err != nil {
		return err
	}
	err = r.RunWithOptions(ctx, "devices", RunOptions{Settings: settings, ChangedDeviceIPs: changedIPs})
	if err == nil {
		return nil
	}
	restoreErr := writeDeviceList(r.deviceExclusionsPath, previous)
	if restoreErr == nil {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		restoreErr = r.RunWithOptions(rollbackCtx, "devices", RunOptions{Settings: settings, ChangedDeviceIPs: changedIPs})
	}
	if restoreErr != nil {
		return errors.Join(err, fmt.Errorf("restore device exclusions: %w", restoreErr))
	}
	return err
}

func writeDeviceList(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".device-macs-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
