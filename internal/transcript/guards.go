package transcript

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/danielfnfaria/agtop/internal/domain"
)

// Narrow readers of the JSONL fields in use (the format is not public). They never panic: a
// missing field or one of an unexpected type reads as absent.

type record = map[string]any

func asRecord(v any) (record, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func getString(r record, key string) (string, bool) {
	s, ok := r[key].(string)
	return s, ok
}

func getNonEmptyString(r record, key string) (string, bool) {
	s, ok := getString(r, key)
	return s, ok && s != ""
}

// getNumber reads a finite number: float64 as decoded by encoding/json, or an integer or
// json.Number from callers that build the value themselves.
func getNumber(r record, key string) (float64, bool) {
	var f float64
	switch n := r[key].(type) {
	case float64:
		f = n
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

func getBoolean(r record, key string) (value, ok bool) {
	value, ok = r[key].(bool)
	return value, ok
}

func isTrue(r record, key string) bool {
	v, ok := getBoolean(r, key)
	return ok && v
}

func getRecord(r record, key string) (record, bool) {
	return asRecord(r[key])
}

func getArray(r record, key string) ([]any, bool) {
	a, ok := r[key].([]any)
	return a, ok
}

// getTimestamp reads an ISO 8601 timestamp as epoch ms; 0 if absent or invalid.
func getTimestamp(r record, key string) int64 {
	s, ok := getString(r, key)
	if !ok {
		return 0
	}
	ms, ok := domain.ParseDate(s)
	if !ok {
		return 0
	}
	return ms
}

// jsNumberString formats a number as JavaScript's String(n).
func jsNumberString(n float64) string {
	if n == 0 {
		return "0"
	}
	if a := math.Abs(n); a >= 1e21 || a < 1e-6 {
		s := strconv.FormatFloat(n, 'e', -1, 64)
		mant, exp, _ := strings.Cut(s, "e")
		sign, digits := exp[:1], strings.TrimLeft(exp[1:], "0")
		return mant + "e" + sign + digits
	}
	return strconv.FormatFloat(n, 'f', -1, 64)
}
