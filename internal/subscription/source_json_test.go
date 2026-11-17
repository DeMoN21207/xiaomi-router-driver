package subscription

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

const xraySubscriptionFixture = `[
 {"remarks":"Germany","outbounds":[
  {"tag":"proxy","protocol":"vless","settings":{"address":"de.example.com","port":443,"id":"11111111-1111-1111-1111-111111111111","encryption":"none","flow":"xtls-rprx-vision"},"streamSettings":{"network":"tcp","security":"reality","realitySettings":{"serverName":"cdn.example.com","fingerprint":"firefox","publicKey":"test-public-key","shortId":"abcd"},"tcpSettings":{"header":{"type":"none"}}}},
  {"tag":"direct","protocol":"freedom"},{"tag":"block","protocol":"blackhole"}]},
 {"remarks":"Netherlands","outbounds":[
  {"tag":"proxy","protocol":"vless","settings":{"vnext":[{"address":"nl.example.com","port":8443,"users":[{"id":"22222222-2222-2222-2222-222222222222","encryption":"none"}]}]},"streamSettings":{"network":"tcp","security":"tls","tlsSettings":{"serverName":"nl.example.com","alpn":["h2","http/1.1"]}}}]},
 {"remarks":"Reserve","outbounds":[
  {"tag":"proxy","protocol":"hysteria","settings":{"address":"hy.example.com","port":27015,"version":2},"streamSettings":{"network":"hysteria","security":"tls","hysteriaSettings":{"version":2,"auth":"test-auth"},"tlsSettings":{"serverName":"hy.example.com","alpn":["h3"]},"finalmask":{"udp":[{"type":"salamander","settings":{"password":"test-obfs"}}]}}}]}
]`

func TestParseEntriesXrayJSON(t *testing.T) {
	want := []Entry{
		{Name: "Germany", Address: "de.example.com:443", Type: "vless", Outbound: map[string]any{
			"type": "vless", "server": "de.example.com", "server_port": 443,
			"uuid": "11111111-1111-1111-1111-111111111111", "flow": "xtls-rprx-vision",
			"tls": map[string]any{"enabled": true, "server_name": "cdn.example.com",
				"utls":    map[string]any{"enabled": true, "fingerprint": "firefox"},
				"reality": map[string]any{"enabled": true, "public_key": "test-public-key", "short_id": "abcd"}},
		}},
		{Name: "Netherlands", Address: "nl.example.com:8443", Type: "vless", Outbound: map[string]any{
			"type": "vless", "server": "nl.example.com", "server_port": 8443,
			"uuid": "22222222-2222-2222-2222-222222222222",
			"tls":  map[string]any{"enabled": true, "server_name": "nl.example.com", "alpn": []string{"h2", "http/1.1"}},
		}},
		{Name: "Reserve", Address: "hy.example.com:27015", Type: "hysteria2", Outbound: map[string]any{
			"type": "hysteria2", "server": "hy.example.com", "server_port": 27015, "password": "test-auth",
			"tls":  map[string]any{"enabled": true, "server_name": "hy.example.com", "alpn": []string{"h3"}},
			"obfs": map[string]any{"type": "salamander", "password": "test-obfs"},
		}},
	}
	for name, payload := range map[string]string{
		"plain":  xraySubscriptionFixture,
		"base64": base64.StdEncoding.EncodeToString([]byte(xraySubscriptionFixture)),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseEntries(payload)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("entries = %#v, want %#v", got, want)
			}
		})
	}
}

func TestParseEntriesXrayRejectsLossyConversions(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		value       any
	}{
		{"tcp disguise", "streamSettings", map[string]any{"network": "tcp", "tcpSettings": map[string]any{"header": map[string]any{"type": "http"}}}},
		{"chained outbound", "proxySettings", map[string]any{"tag": "upstream"}},
		{"missing credentials", "settings", map[string]any{"address": "de.example.com", "port": 443}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var configs []map[string]any
			if err := json.Unmarshal([]byte(xraySubscriptionFixture), &configs); err != nil {
				t.Fatal(err)
			}
			outbound := configs[0]["outbounds"].([]any)[0].(map[string]any)
			outbound[tc.field] = tc.value
			payload, err := json.Marshal(configs[0])
			if err != nil {
				t.Fatal(err)
			}
			if got, err := ParseEntries(string(payload)); err == nil {
				t.Fatalf("unsupported config accepted: %+v", got)
			}
		})
	}
}

func compatibilityWarningFixture() string {
	const endpoint = "vless://11111111-1111-1111-1111-111111111111@real.example.com:443?security=tls#"
	return endpoint + "Клиент устарел / не поддерживает JSON\n" + endpoint + "Часть резервов будет недоступна\n" + endpoint + "Real"
}

func TestParseEntriesDropsNamedCompatibilityWarnings(t *testing.T) {
	got, err := ParseEntries(compatibilityWarningFixture())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Real" {
		t.Fatalf("warnings appeared as endpoints: %+v", got)
	}
}

func TestFetchEntriesPrefersXrayJSONOverLegacyWarnings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.UserAgent(), "Throne/1.4.") && strings.Contains(r.Header.Get("Accept"), "application/json") {
			_, _ = w.Write([]byte(xraySubscriptionFixture))
		} else {
			_, _ = w.Write([]byte(compatibilityWarningFixture()))
		}
	}))
	defer server.Close()
	got, err := FetchEntries(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[2].Type != "hysteria2" {
		t.Fatalf("did not obtain JSON reserves: %+v", got)
	}
}

func TestFetchEntriesCachedReplacesLegacyWarningCache(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(xraySubscriptionFixture))
	}))
	defer server.Close()
	dir := t.TempDir()
	if err := saveEntriesCache(entriesCachePath(dir, server.URL), server.URL, compatibilityWarningFixture(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, mode, err := FetchEntriesCached(server.URL, dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode != entriesFetchLive || len(got) != 3 {
		t.Fatalf("legacy cache was reused: mode=%s, entries=%+v", mode, got)
	}
	cached, err := LoadCachedEntries(server.URL, dir)
	if err != nil || len(cached) != 3 {
		t.Fatalf("JSON cache was not saved: entries=%+v, err=%v", cached, err)
	}
}

func TestFetchEntriesKeepsUsableLegacyFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(compatibilityWarningFixture()))
	}))
	defer server.Close()
	got, err := FetchEntries(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Real" {
		t.Fatalf("unexpected legacy fallback: %+v", got)
	}
}

func TestFetchEntriesRetriesMixedWarningResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() == "Happ" {
			_, _ = w.Write([]byte(xraySubscriptionFixture))
		} else {
			_, _ = w.Write([]byte(compatibilityWarningFixture()))
		}
	}))
	defer server.Close()
	got, err := FetchEntries(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[2].Name != "Reserve" {
		t.Fatalf("stopped at a partial legacy response: %+v", got)
	}
}

func TestParseEntriesSingleXrayConfig(t *testing.T) {
	var configs []json.RawMessage
	if err := json.Unmarshal([]byte(xraySubscriptionFixture), &configs); err != nil {
		t.Fatal(err)
	}
	got, err := ParseEntries(string(configs[0]))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Germany" || got[0].Address != "de.example.com:443" {
		t.Fatalf("unexpected single config entries: %+v", got)
	}
}
