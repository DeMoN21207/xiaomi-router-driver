package config

import (
	"errors"
	"net"
	"strings"
)

type DeviceExclusion struct {
	MAC  string `json:"mac"`
	Name string `json:"name"`
}

func NormalizeDeviceMAC(raw string) (string, error) {
	mac, err := net.ParseMAC(strings.TrimSpace(raw))
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 || mac.String() == "00:00:00:00:00:00" {
		return "", errors.New("invalid device MAC address")
	}
	return mac.String(), nil
}

func DeviceExcluded(items []DeviceExclusion, mac string) bool {
	normalized, err := NormalizeDeviceMAC(mac)
	if err != nil {
		return false
	}
	for _, item := range items {
		if item.MAC == normalized {
			return true
		}
	}
	return false
}

func (m *Manager) DeviceExclusions() ([]DeviceExclusion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureReadyUnlocked(); err != nil {
		return nil, err
	}
	return m.deviceExclusionsUnlocked()
}

func (m *Manager) deviceExclusionsUnlocked() ([]DeviceExclusion, error) {
	rows, err := m.db.Query(`SELECT mac, name FROM device_exclusions ORDER BY mac`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []DeviceExclusion{}
	for rows.Next() {
		var item DeviceExclusion
		if err := rows.Scan(&item.MAC, &item.Name); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (m *Manager) SetDeviceExclusion(mac, name string, excluded bool) error {
	normalized, err := NormalizeDeviceMAC(mac)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) > 160 {
		return errors.New("device name is too long")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureReadyUnlocked(); err != nil {
		return err
	}
	if excluded {
		_, err = m.db.Exec(`INSERT INTO device_exclusions (mac,name) VALUES (?,?) ON CONFLICT(mac) DO UPDATE SET name=excluded.name`, normalized, name)
	} else {
		_, err = m.db.Exec(`DELETE FROM device_exclusions WHERE mac=?`, normalized)
	}
	return err
}
