package subscription

import (
	"errors"
	"strings"
)

// UsesXray identifies an outbound retained in Xray's native schema.
func (e Entry) UsesXray() bool { return stringify(e.Outbound["protocol"]) != "" }

func parseNativeXrayOutbound(raw map[string]any) (Entry, error) {
	unsupported := errors.New("unsupported native Xray endpoint")
	if stringify(raw["protocol"]) != "vless" || raw["proxySettings"] != nil {
		return Entry{}, unsupported
	}
	settings, _ := raw["settings"].(map[string]any)
	stream, _ := raw["streamSettings"].(map[string]any)
	sockopt, _ := stream["sockopt"].(map[string]any)
	if stringify(sockopt["dialerProxy"]) != "" {
		return Entry{}, unsupported
	}
	endpoint, user := settings, settings
	if vnext, ok := settings["vnext"].([]any); ok {
		if len(vnext) != 1 {
			return Entry{}, unsupported
		}
		endpoint, _ = vnext[0].(map[string]any)
		users, _ := endpoint["users"].([]any)
		if len(users) != 1 {
			return Entry{}, unsupported
		}
		user, _ = users[0].(map[string]any)
	}
	server, port := stringify(endpoint["address"]), intValue(endpoint["port"])
	if server == "" || port <= 0 || port > 65535 || stringify(user["id"]) == "" || user["reverse"] != nil {
		return Entry{}, unsupported
	}
	network, security := stringify(stream["network"]), stringify(stream["security"])
	if security != "" && security != "none" && security != "tls" && security != "reality" {
		return Entry{}, unsupported
	}
	// This fallback is for the native features we cannot translate. Keep
	// unrelated malformed/unsupported configurations out of the catalogue.
	encryption := stringify(user["encryption"])
	if network != "xhttp" && (encryption == "" || encryption == "none") {
		return Entry{}, unsupported
	}
	switch network {
	case "", "tcp", "raw", "xhttp":
	default:
		return Entry{}, unsupported
	}
	return Entry{Name: firstNonEmpty(stringify(raw["tag"]), server), Address: formatAddress(server, port), Type: "vless", Outbound: cloneMap(raw)}, nil
}

func buildXrayTUNConfig(interfaceName string, index int, outbound map[string]any) map[string]any {
	return xrayConfig(map[string]any{
		"protocol": "tun", "tag": "tun-in", "settings": map[string]any{
			"name": interfaceName, "mtu": 1400, "gateway": []string{tunAddress(index)},
			"autoSystemRoutingTable": []string{}, "autoSystemDnsToGateway": false,
		},
	}, outbound)
}

func xrayConfig(inbound map[string]any, outbound map[string]any) map[string]any {
	return map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"inbounds":  []any{inbound},
		"outbounds": []any{cloneMap(outbound)},
	}
}

// BuildProbeConfig uses a loopback-only listener without changing routes.
func BuildProbeConfig(entry Entry, port int) map[string]any {
	if entry.UsesXray() {
		return xrayConfig(map[string]any{"protocol": "socks", "listen": "127.0.0.1", "port": port,
			"settings": map[string]any{"auth": "noauth", "udp": true}}, entry.Outbound)
	}
	outbound := cloneMap(entry.Outbound)
	outbound["tag"] = "proxy"
	return map[string]any{
		"log":       map[string]any{"disabled": true},
		"inbounds":  []any{map[string]any{"type": "socks", "listen": "127.0.0.1", "listen_port": port}},
		"outbounds": []any{outbound},
		"route":     map[string]any{"final": "proxy", "auto_detect_interface": true},
	}
}

func (m *Manager) runtimeBinary(configPath string) string {
	if strings.HasSuffix(configPath, ".xray.json") {
		return m.xrayBinary
	}
	return m.singBoxBinary
}
