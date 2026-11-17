package status

import (
	"bufio"
	"errors"
	"io"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"xiomi-router-driver/internal/config"
)

type NetworkDevice struct {
	MAC      string   `json:"mac"`
	Name     string   `json:"name"`
	IPs      []string `json:"ips"`
	Present  bool     `json:"present"`
	Excluded bool     `json:"excluded"`
}

func NetworkDevices(lanIface string, exclusions []config.DeviceExclusion) ([]NetworkDevice, error) {
	readers := make([]io.Reader, 2)
	for i, path := range []string{"/tmp/dhcp.leases", "/proc/net/arp"} {
		f, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			readers[i] = strings.NewReader("")
			continue
		}
		if err != nil {
			return nil, err
		}
		defer f.Close()
		readers[i] = f
	}
	return parseNetworkDevices(readers[0], readers[1], lanIface, time.Now().Unix(), exclusions)
}

func parseNetworkDevices(leases, arp io.Reader, lanIface string, now int64, exclusions []config.DeviceExclusion) ([]NetworkDevice, error) {
	// ARP is the current owner of an IPv4 address; a DHCP lease can be stale.
	type identity struct{ mac, name string }
	byIP := map[string]identity{}
	names := map[string]string{}
	scanner := bufio.NewScanner(leases)
	for scanner.Scan() {
		f := strings.Fields(scanner.Text())
		if len(f) < 4 {
			continue
		}
		expires, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil || (expires != 0 && expires <= now) {
			continue
		}
		mac, err := config.NormalizeDeviceMAC(f[1])
		if err != nil {
			continue
		}
		ip, err := netip.ParseAddr(f[2])
		if err != nil || !ip.Is4() || !ip.IsPrivate() {
			continue
		}
		name := f[3]
		if name == "*" {
			name = ""
		}
		byIP[ip.String()] = identity{mac, name}
		names[mac] = name
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	scanner = bufio.NewScanner(arp)
	for scanner.Scan() {
		f := strings.Fields(scanner.Text())
		if len(f) < 6 || f[5] != lanIface {
			continue
		}
		flags, err := strconv.ParseUint(f[2], 0, 32)
		if err != nil || flags&2 == 0 {
			continue
		}
		mac, err := config.NormalizeDeviceMAC(f[3])
		if err != nil {
			continue
		}
		ip, err := netip.ParseAddr(f[0])
		if err != nil || !ip.Is4() || !ip.IsPrivate() {
			continue
		}
		byIP[ip.String()] = identity{mac, names[mac]}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	byMAC := map[string]*NetworkDevice{}
	for ip, id := range byIP {
		d := byMAC[id.mac]
		if d == nil {
			d = &NetworkDevice{MAC: id.mac, Name: id.name, IPs: []string{}, Present: true}
			byMAC[id.mac] = d
		}
		d.IPs = append(d.IPs, ip)
	}
	for _, ex := range exclusions {
		d := byMAC[ex.MAC]
		if d == nil {
			d = &NetworkDevice{MAC: ex.MAC, IPs: []string{}}
			byMAC[ex.MAC] = d
		}
		d.Excluded = true
		if d.Name == "" {
			d.Name = ex.Name
		}
	}
	result := make([]NetworkDevice, 0, len(byMAC))
	for _, d := range byMAC {
		sort.Strings(d.IPs)
		result = append(result, *d)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Excluded != result[j].Excluded {
			return result[i].Excluded
		}
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].MAC < result[j].MAC
	})
	return result, nil
}
