package domain

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// JSWS is the \s class of JavaScript regular expressions (Go's \s is ASCII only).
const JSWS = `[` + jsWSChars + `]`

// JSNotWS is the \S class of JavaScript regular expressions.
const JSNotWS = `[^` + jsWSChars + `]`

const jsWSChars = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// jsISODateRE is the ISO 8601 subset Date.parse accepts (plus its lenient lowercase `t`/`z`, a
// space separator and an offset without a colon). Other formats of the JavaScript fallback
// parser (e.g. "Jan 1 2026") are not read: transcripts always use ISO 8601.
var jsISODateRE = regexp.MustCompile(
	`^([+-]\d{6}|\d{4})(?:-(\d{2})(?:-(\d{2}))?)?` +
		`(?:[Tt ](\d{2}):(\d{2})(?::(\d{2})(?:\.(\d+))?)?([Zz]|[+-]\d{2}:?\d{2})?)?$`)

// ParseDate converts an ISO 8601 date as Date.parse does: a date alone is UTC, a date-time
// without offset is local time, and out-of-range days roll over (Feb 30 is Mar 2).
func ParseDate(s string) (int64, bool) {
	m := jsISODateRE.FindStringSubmatch(strings.TrimFunc(s, IsJSSpace))
	if m == nil {
		return 0, false
	}
	num := func(v string, def int) int {
		if v == "" {
			return def
		}
		n, _ := strconv.Atoi(strings.TrimPrefix(v, "+"))
		return n
	}
	year, month, day := num(m[1], 0), num(m[2], 1), num(m[3], 1)
	hour, minute, sec := num(m[4], 0), num(m[5], 0), num(m[6], 0)
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 24 || minute > 59 || sec > 59 {
		return 0, false
	}
	if hour == 24 && (minute != 0 || sec != 0 || strings.Trim(m[7], "0") != "") {
		return 0, false
	}
	nanos := 0
	if frac := m[7]; frac != "" {
		frac = (frac + "000")[:3]
		nanos = num(frac, 0) * int(time.Millisecond)
	}
	loc := time.UTC
	switch zone := m[8]; {
	case zone == "" && m[4] != "":
		loc = time.Local
	case zone != "" && zone != "Z" && zone != "z":
		sign := 1
		if zone[0] == '-' {
			sign = -1
		}
		digits := strings.ReplaceAll(zone[1:], ":", "")
		oh, om := num(digits[:2], 0), num(digits[2:], 0)
		if oh > 23 || om > 59 {
			return 0, false
		}
		loc = time.FixedZone("", sign*(oh*3600+om*60))
	}
	t := time.Date(year, time.Month(month), day, hour, minute, sec, nanos, loc)
	return t.UnixMilli(), true
}

// IsJSSpace is the white space of String.prototype.trim.
// IsJSSpace reports JavaScript white space.
func IsJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}
