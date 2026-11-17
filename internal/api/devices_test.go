package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/routing"
)

func TestDeviceEndpointRejectsBusyRouterWithoutQueuedMutation(t *testing.T) {
	dir := t.TempDir()
	h := NewHandler(Dependencies{State: config.NewManager(openAPITestDB(t, filepath.Join(dir, "db")), "")})
	h.applyMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/devices", strings.NewReader(`{"mac":"02:00:00:00:00:51","excluded":true}`)).WithContext(ctx))
		close(done)
	}()
	select {
	case <-done:
		h.applyMu.Unlock()
		if rec.Code != http.StatusConflict {
			t.Fatalf("busy status: %d", rec.Code)
		}
	case <-ctx.Done():
		h.applyMu.Unlock()
		<-done
		t.Fatal("device request queued behind routing operation")
	}
}

func TestDeviceEndpointToggleRestoreAndFailure(t *testing.T) {
	dir := t.TempDir()
	state := config.NewManager(openAPITestDB(t, filepath.Join(dir, "db")), "")
	script := filepath.Join(dir, "routes.sh")
	os.WriteFile(script, []byte("#!/bin/sh\n[ ! -f \"$DEVICE_API_FAIL\" ]\n"), 0700)
	t.Setenv("DEVICE_API_FAIL", filepath.Join(dir, "fail"))
	runner := routing.NewRunner(script)
	runner.SetDeviceExclusionsPath(filepath.Join(dir, "macs"))
	h := NewHandler(Dependencies{State: state, Routing: runner, DataDir: dir})
	send := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/devices", strings.NewReader(body)))
		return rec
	}
	body := `{"mac":"02:00:00:00:00:51","name":"Phone","excluded":true}`
	if rec := send(body); rec.Code != 200 {
		t.Fatalf("exclude: %d %s", rec.Code, rec.Body.String())
	}
	loaded, _ := state.Load()
	if len(loaded.ExcludedDevices) != 1 {
		t.Fatal("exception not persisted")
	}
	if rec := send(`{"mac":"02:00:00:00:00:51","excluded":false}`); rec.Code != 200 {
		t.Fatalf("restore: %s", rec.Body.String())
	}
	loaded, _ = state.Load()
	if len(loaded.ExcludedDevices) != 0 {
		t.Fatal("exception not removed")
	}
	os.WriteFile(filepath.Join(dir, "fail"), nil, 0600)
	if rec := send(body); rec.Code != http.StatusInternalServerError {
		t.Fatalf("failed apply: %d %s", rec.Code, rec.Body.String())
	}
	loaded, _ = state.Load()
	if len(loaded.ExcludedDevices) != 0 {
		t.Fatal("failed apply persisted exception")
	}
	if rec := send(`{"mac":"invalid","excluded":true}`); rec.Code != 400 {
		t.Fatalf("invalid MAC: %d", rec.Code)
	}
	if rec := send(`{"mac":"02:00:00:00:00:51"}`); rec.Code != 400 {
		t.Fatalf("missing explicit choice: %d", rec.Code)
	}
}
