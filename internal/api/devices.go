package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/status"
)

func (h *Handler) handleDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		writeMethodNotAllowed(w, http.MethodGet, http.MethodPut)
		return
	}
	if r.Method == http.MethodGet {
		state, err := h.state.Load()
		if err != nil {
			writeError(w, 500, err)
			return
		}
		h.writeDevices(w, state)
		return
	}
	var request struct {
		MAC      string `json:"mac"`
		Name     string `json:"name"`
		Excluded *bool  `json:"excluded"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, 400, err)
		return
	}
	mac, err := config.NormalizeDeviceMAC(request.MAC)
	if err != nil || request.Excluded == nil || len([]rune(request.Name)) > 160 {
		writeError(w, 400, errors.New("valid mac, name and explicit excluded flag are required"))
		return
	}
	if !h.applyMu.TryLock() {
		writeError(w, http.StatusConflict, errors.New("Роутер применяет другие настройки. Повторите переключение через несколько секунд."))
		return
	}
	defer h.applyMu.Unlock()
	state, err := h.state.Load()
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if config.DeviceExcluded(state.ExcludedDevices, mac) == *request.Excluded {
		h.writeDevices(w, state)
		return
	}
	if h.routing == nil {
		writeError(w, 503, errors.New("routing is unavailable"))
		return
	}
	inventory, err := status.NetworkDevices(state.Routing.LANIface, state.ExcludedDevices)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	var ips []string
	for _, item := range inventory {
		if item.MAC == mac {
			ips = item.IPs
			if request.Name == "" {
				request.Name = item.Name
			}
			break
		}
	}
	next := make([]config.DeviceExclusion, 0, len(state.ExcludedDevices)+1)
	for _, item := range state.ExcludedDevices {
		if item.MAC != mac {
			next = append(next, item)
		}
	}
	if *request.Excluded {
		next = append(next, config.DeviceExclusion{MAC: mac, Name: request.Name})
	}
	// Finish the transaction even if reconnecting this device closes its HTTP request.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 60*time.Second)
	defer cancel()
	if err := h.routing.ApplyDeviceExclusions(ctx, state.Routing, next, ips); err != nil {
		writeError(w, 500, err)
		return
	}
	if err := h.state.SetDeviceExclusion(mac, request.Name, *request.Excluded); err != nil {
		rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer rollbackCancel()
		restoreErr := h.routing.ApplyDeviceExclusions(rollbackCtx, state.Routing, state.ExcludedDevices, ips)
		writeError(w, 500, errors.Join(err, restoreErr))
		return
	}
	state.ExcludedDevices = next
	mode := "routes"
	if *request.Excluded {
		mode = "direct"
	}
	h.recordEvent("info", "device.routing_changed", "Device "+mac+": "+mode)
	h.writeDevices(w, state)
}

func (h *Handler) writeDevices(w http.ResponseWriter, state config.State) {
	devices, err := status.NetworkDevices(state.Routing.LANIface, state.ExcludedDevices)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"devices": devices, "defaultMode": "routes", "ipv6Mode": state.Routing.IPv6Mode})
}
