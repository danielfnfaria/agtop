package domain

import (
	"encoding/json"
	"math"
	"slices"
)

// Helpers over values decoded with encoding/json into `any`, for the strict validators.

const (
	// maxEpochMs is the latest accepted epoch (ms): year 2200.
	maxEpochMs = 7_258_118_400_000
	// msThreshold: epoch values below it are seconds, at or above it milliseconds.
	msThreshold = 1e12
)

func asRecord(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func onlyKeys(m map[string]any, allowed ...string) bool {
	for k := range m {
		if !slices.Contains(allowed, k) {
			return false
		}
	}
	return true
}

// asNumber returns the value as a finite float64 if it is a JSON number.
func asNumber(v any) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case float32:
		f = float64(n)
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	case json.Number:
		var err error
		if f, err = n.Float64(); err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return f, !math.IsNaN(f) && !math.IsInf(f, 0)
}

func asEpoch(v any) (int64, bool) {
	f, ok := asNumber(v)
	if !ok || f <= 0 || f > maxEpochMs {
		return 0, false
	}
	return int64(f), true
}
