package config

import "strings"

const PrioritySelectionLatency = "latency"
const PrioritySelectionOrder = "priority"

func NormalizePrioritySelectionMode(value string) string {
	if strings.ToLower(strings.TrimSpace(value)) == PrioritySelectionLatency {
		return PrioritySelectionLatency
	}
	return PrioritySelectionOrder
}
