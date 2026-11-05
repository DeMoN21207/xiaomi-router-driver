package status

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDeviceHistoryBucketsRespectSamplingCadence(t *testing.T) {
	from := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		rangeName string
		want      time.Duration
	}{{"1h", 2 * time.Minute}, {"3h", 6 * time.Minute}} {
		t.Run(tc.rangeName, func(t *testing.T) {
			spec, _ := parseDeviceTrafficHistoryRange(tc.rangeName)
			history := aggregateDeviceTrafficHistory([]deviceTrafficHistorySample{{At: from, Bytes: 100, TunneledBytes: 100}}, spec, from, from.Add(spec.Lookback), 2*time.Minute, "192.168.1.2")
			if history.BucketSeconds != int(tc.want.Seconds()) {
				t.Fatalf("bucket seconds = %d, want %v", history.BucketSeconds, tc.want.Seconds())
			}
			var total uint64
			for _, point := range history.Points {
				total += point.Bytes
			}
			if total != history.TotalBytes || total != 100 {
				t.Fatalf("point total = %d, response = %d", total, history.TotalBytes)
			}
		})
	}
}

func TestDeviceHistoryIncludesExactWindowWithoutSamples(t *testing.T) {
	from := time.Date(2026, 11, 5, 12, 3, 17, 0, time.UTC)
	to := from.Add(43 * time.Minute)
	spec, _ := parseDeviceTrafficHistoryRange("1h")
	for _, history := range []DeviceTrafficHistoryResponse{
		aggregateDeviceTrafficHistory(nil, spec, from, to, 2*time.Minute, "192.168.1.2"),
		func() DeviceTrafficHistoryResponse {
			result, err := (&Service{}).deviceTrafficHistoryWindow("192.168.1.2", spec, from, to)
			if err != nil {
				t.Fatal(err)
			}
			return result
		}(),
	} {
		raw, _ := json.Marshal(history)
		var fields map[string]any
		json.Unmarshal(raw, &fields)
		if fields["from"] != from.Format(time.RFC3339) || fields["to"] != to.Format(time.RFC3339) {
			t.Fatalf("missing exact window: from=%v to=%v", fields["from"], fields["to"])
		}
	}
}
