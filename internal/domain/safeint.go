package domain

import "math"

// MaxSafeInt is JavaScript's Number.MAX_SAFE_INTEGER (2^53-1).
const MaxSafeInt = 1<<53 - 1

// SafeInt converts a float to an int the way a JS safe integer reads: NaN is 0, the value is
// truncated and clamped to [-MaxSafeInt, MaxSafeInt], so the result is platform independent.
func SafeInt(f float64) int {
	if math.IsNaN(f) {
		return 0
	}
	return int(math.Max(-MaxSafeInt, math.Min(MaxSafeInt, math.Trunc(f))))
}

// SafeNonNegInt is SafeInt with negatives (and NaN) read as 0.
func SafeNonNegInt(f float64) int {
	if f < 0 {
		return 0
	}
	return SafeInt(f)
}

// SatAdd adds two counters, saturating in [0, MaxSafeInt] instead of wrapping.
func SatAdd(a, b int) int {
	s := a + b // both within +-2^53 after clamping, so no int64 overflow
	if s < 0 {
		return 0
	}
	if s > MaxSafeInt {
		return MaxSafeInt
	}
	return s
}
