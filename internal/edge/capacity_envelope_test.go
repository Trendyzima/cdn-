package edge

import (
	"math"
	"testing"
)

// requiredViewerEgressGbps models payload egress only. It deliberately does
// not infer achievable capacity from logical request fan-out or cache hits.
func requiredViewerEgressGbps(viewers int64, bitrateMbps float64) (float64, bool) {
	if viewers <= 0 || bitrateMbps <= 0 || math.IsNaN(bitrateMbps) || math.IsInf(bitrateMbps, 0) {
		return 0, false
	}
	return float64(viewers) * bitrateMbps / 1000, true
}

// minimumEdgeNodes uses a measured sustained per-node egress value and caps
// planned utilization to leave operational headroom. It is a sizing model,
// not a substitute for a distributed network load test.
func minimumEdgeNodes(requiredGbps, measuredNodeGbps, maxUtilization float64) (int, bool) {
	if requiredGbps <= 0 || measuredNodeGbps <= 0 || maxUtilization <= 0 || maxUtilization >= 1 ||
		math.IsNaN(requiredGbps) || math.IsNaN(measuredNodeGbps) || math.IsNaN(maxUtilization) ||
		math.IsInf(requiredGbps, 0) || math.IsInf(measuredNodeGbps, 0) || math.IsInf(maxUtilization, 0) {
		return 0, false
	}
	return int(math.Ceil(requiredGbps / (measuredNodeGbps * maxUtilization))), true
}

func TestCapacityEnvelope100kViewersAt2Mbps(t *testing.T) {
	required, ok := requiredViewerEgressGbps(100_000, 2)
	if !ok || required != 200 {
		t.Fatalf("required egress = %.3f Gbit/s, valid=%v; want 200 Gbit/s", required, ok)
	}
	// 200 Gbit/s / 8 * 3600 seconds = 90,000 decimal GB per hour.
	terabytesPerHour := required * 1000 / 8 * 3600 / 1_000_000
	if math.Abs(terabytesPerHour-90) > 0.000001 {
		t.Fatalf("payload egress = %.3f TB/hour, want 90 TB/hour", terabytesPerHour)
	}
}

func TestCapacityEnvelopeEdgeFleetSizingUsesMeasuredThroughput(t *testing.T) {
	required, _ := requiredViewerEgressGbps(100_000, 2)
	nodes, ok := minimumEdgeNodes(required, 20, 0.70)
	if !ok || nodes != 15 {
		t.Fatalf("20 Gbit/s measured nodes at 70%% utilization => %d nodes, valid=%v; want 15 active nodes", nodes, ok)
	}
	// N+1 is a separate availability allowance, not silently hidden in the
	// throughput formula.
	if nodes+1 != 16 {
		t.Fatalf("N+1 fleet = %d, want 16", nodes+1)
	}
}

func TestCapacityEnvelopeRejectsInvalidSizingInputs(t *testing.T) {
	for _, tc := range []struct {
		viewers int64
		bitrate float64
	}{
		{0, 2}, {-1, 2}, {100, 0}, {100, -1}, {100, math.NaN()}, {100, math.Inf(1)},
	} {
		if _, ok := requiredViewerEgressGbps(tc.viewers, tc.bitrate); ok {
			t.Fatalf("accepted invalid viewer/bitrate inputs: %+v", tc)
		}
	}
	for _, tc := range []struct{ required, node, utilization float64 }{
		{0, 20, .7}, {200, 0, .7}, {200, 20, 0}, {200, 20, 1}, {200, 20, math.NaN()},
	} {
		if _, ok := minimumEdgeNodes(tc.required, tc.node, tc.utilization); ok {
			t.Fatalf("accepted invalid fleet sizing inputs: %+v", tc)
		}
	}
}
