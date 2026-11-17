package subscription

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

// Xray subscriptions contain complete configs, often with the same "proxy"
// tag in every config. Only import endpoint settings; keep our own DNS,
// routing and inbounds, and use each config's remarks as its location name.
func parseXrayEntries(payload string) ([]Entry, error) {
	var raw any
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	configs, ok := raw.([]any)
	if !ok {
		configs = []any{raw}
	}
	var entries []Entry
	seen := make(map[string]bool)
	for _, item := range configs {
		config, ok := item.(map[string]any)
		if !ok {
			continue
		}
		outbounds, ok := config["outbounds"].([]any)
		if !ok {
			outbounds = []any{config}
		}
		var locations []Entry
		for _, item := range outbounds {
			outbound, _ := item.(map[string]any)
			entry, err := parseXrayOutbound(outbound)
			if err == nil {
				locations = append(locations, entry)
			}
		}
		for _, entry := range locations {
			if remarks := stringify(config["remarks"]); remarks != "" {
				if len(locations) == 1 {
					entry.Name = remarks
				} else {
					entry.Name = remarks + " / " + entry.Name
				}
			}
			if !seen[entry.Name] && !isCompatibilityPlaceholderEntry(entry) {
				seen[entry.Name] = true
				entries = append(entries, entry)
			}
		}
	}
	if len(entries) == 0 {
		return nil, errors.New("no supported Xray endpoints found")
	}
	return entries, nil
}

func parseXrayOutbound(raw map[string]any) (Entry, error) {
	entry, err := parseXraySingBoxOutbound(raw)
	if err == nil {
		return entry, nil
	}
	return parseNativeXrayOutbound(raw)
}

func parseXraySingBoxOutbound(raw map[string]any) (Entry, error) {
	unsupported := errors.New("Xray endpoint cannot be represented by sing-box")
	kind := stringify(raw["protocol"])
	if kind != "vless" && kind != "hysteria" {
		return Entry{}, unsupported
	}
	settings, _ := raw["settings"].(map[string]any)
	stream, _ := raw["streamSettings"].(map[string]any)
	mux, _ := raw["mux"].(map[string]any)
	sockopt, _ := stream["sockopt"].(map[string]any)
	if raw["proxySettings"] != nil || boolValue(mux["enabled"]) || stringify(sockopt["dialerProxy"]) != "" {
		return Entry{}, unsupported
	}
	endpoint, credentials := settings, settings
	// Older Xray versions use vnext[].users[] instead of flat VLESS settings.
	if vnext, ok := settings["vnext"].([]any); ok {
		if len(vnext) != 1 {
			return Entry{}, unsupported
		}
		endpoint, _ = vnext[0].(map[string]any)
		users, _ := endpoint["users"].([]any)
		if len(users) != 1 {
			return Entry{}, unsupported
		}
		credentials, _ = users[0].(map[string]any)
	}
	server, port := stringify(endpoint["address"]), intValue(endpoint["port"])
	if server == "" || port <= 0 || port > 65535 {
		return Entry{}, unsupported
	}
	outbound := map[string]any{"type": kind, "server": server, "server_port": port}
	network := stringify(stream["network"])
	security := stringify(stream["security"])
	if security != "" && security != "none" && security != "tls" && security != "reality" {
		return Entry{}, unsupported
	}
	query := make(url.Values)
	tlsSettings, _ := stream[security+"Settings"].(map[string]any)
	for key, target := range map[string]string{"serverName": "sni", "fingerprint": "fp", "publicKey": "pbk", "shortId": "sid"} {
		copyQueryValue(query, target, stringify(tlsSettings[key]))
	}
	if alpn, ok := tlsSettings["alpn"].([]any); ok {
		var protocols []string
		for _, value := range alpn {
			protocols = append(protocols, stringify(value))
		}
		query.Set("alpn", strings.Join(protocols, ","))
	}
	if boolValue(tlsSettings["allowInsecure"]) {
		query.Set("allowInsecure", "1")
	}
	if tls := buildTLS(security, server, query, false); len(tls) > 0 {
		outbound["tls"] = tls
	}
	switch kind {
	case "vless":
		// Silently dropping encryption, masks or XHTTP download settings would
		// produce valid-looking endpoints that cannot connect.
		if encryption := stringify(credentials["encryption"]); encryption != "" && encryption != "none" {
			return Entry{}, unsupported
		}
		if stream["finalmask"] != nil || stringify(credentials["id"]) == "" {
			return Entry{}, unsupported
		}
		switch network {
		case "", "tcp", "raw":
			transportSettings, _ := stream[network+"Settings"].(map[string]any)
			header, _ := transportSettings["header"].(map[string]any)
			if headerType := stringify(header["type"]); headerType != "" && headerType != "none" {
				return Entry{}, unsupported
			}
		default:
			return Entry{}, unsupported
		}
		outbound["uuid"] = stringify(credentials["id"])
		if flow := stringify(credentials["flow"]); flow != "" {
			outbound["flow"] = flow
		}
	case "hysteria":
		hy, _ := stream["hysteriaSettings"].(map[string]any)
		if intValue(settings["version"]) != 2 || intValue(hy["version"]) != 2 || network != "hysteria" || security != "tls" || stringify(hy["auth"]) == "" {
			return Entry{}, unsupported
		}
		kind = "hysteria2"
		outbound["type"] = kind
		outbound["password"] = stringify(hy["auth"])
		if mask, ok := stream["finalmask"].(map[string]any); ok && len(mask) > 0 {
			udp, _ := mask["udp"].([]any)
			if len(mask) != 1 || len(udp) != 1 {
				return Entry{}, unsupported
			}
			obfs, _ := udp[0].(map[string]any)
			options, _ := obfs["settings"].(map[string]any)
			if stringify(obfs["type"]) != "salamander" || stringify(options["password"]) == "" {
				return Entry{}, unsupported
			}
			outbound["obfs"] = map[string]any{"type": "salamander", "password": stringify(options["password"])}
		}
	}
	return Entry{Name: firstNonEmpty(stringify(raw["tag"]), server), Address: formatAddress(server, port), Type: kind, Outbound: outbound}, nil
}
