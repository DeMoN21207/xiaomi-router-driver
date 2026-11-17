package probe

import (
	"context"
	"net"
	"testing"
)

func TestResolvePingTargetRejectsSinkholeAndLoopbackDNS(t *testing.T) {
	for _, addresses := range [][]string{{"0.0.0.0"}, {"127.0.0.1"}, {"::"}, {"::1"}, {"224.0.0.1"}, {"169.254.1.1"}} {
		lookup := func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP(addresses[0])}}, nil
		}
		if target, err := resolvePingTarget(t.Context(), "blocked.example", lookup); err == nil || target != "" {
			t.Fatalf("DNS %v would ping the router: target=%q error=%v", addresses, target, err)
		}
	}
}

func TestResolvePingTargetPinsUsableResolvedAddress(t *testing.T) {
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("::1")}, {IP: net.ParseIP("2001:db8::20")}, {IP: net.ParseIP("203.0.113.20")}}, nil
	}
	target, err := resolvePingTarget(t.Context(), "vpn.example", lookup)
	if err != nil || target != "203.0.113.20" {
		t.Fatalf("target=%q err=%v", target, err)
	}
}

func TestExtractHost(t *testing.T) {
	tests := []struct {
		address string
		want    string
	}{
		{address: "de1.gofizzin.com:443", want: "de1.gofizzin.com"},
		{address: "127.0.0.1:1194", want: "127.0.0.1"},
		{address: "[2001:db8::1]:443", want: "2001:db8::1"},
		{address: "example.org", want: "example.org"},
	}

	for _, test := range tests {
		if got := extractHost(test.address); got != test.want {
			t.Fatalf("extractHost(%q) = %q, want %q", test.address, got, test.want)
		}
	}
}

func TestParsePingLatency(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   int64
	}{
		{
			name:   "linux ping",
			output: "64 bytes from 1.1.1.1: icmp_seq=1 ttl=57 time=28.6 ms",
			want:   28,
		},
		{
			name:   "windows ping",
			output: "Reply from 1.1.1.1: bytes=32 time=17ms TTL=57",
			want:   17,
		},
		{
			name:   "windows average",
			output: "Average = 24ms",
			want:   24,
		},
	}

	for _, test := range tests {
		if got := parsePingLatency(test.output); got != test.want {
			t.Fatalf("%s: parsePingLatency() = %d, want %d", test.name, got, test.want)
		}
	}
}

func TestMeasureLatenciesSkipsEndpointErrors(t *testing.T) {
	locations := MeasureLatencies(t.Context(), []Location{
		{
			Name:          "Broken",
			Address:       "extranetherlands:443",
			Type:          "vless",
			EndpointError: "endpoint host is not routable",
		},
	})

	if len(locations) != 1 {
		t.Fatalf("MeasureLatencies() returned %d locations, want 1", len(locations))
	}
	if locations[0].EndpointError == "" {
		t.Fatalf("MeasureLatencies() cleared endpoint error: %+v", locations[0])
	}
	if locations[0].LatencyError != "" {
		t.Fatalf("MeasureLatencies() set latency error for endpoint issue: %+v", locations[0])
	}
}
