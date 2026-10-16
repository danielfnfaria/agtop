package domain

import (
	"regexp"
	"strings"
)

var (
	// CSI (ESC [ or C1 0x9b) with parameters, intermediates and a final byte.
	csiRE = regexp.MustCompile(`(?:\x{1b}\[|\x{9b})[0-?]*[ -/]*[@-~]`)
	// OSC/DCS/SOS/PM/APC (ESC ], ESC P... or C1) up to BEL or ST.
	stringSeqRE = regexp.MustCompile(`(?:\x{1b}[\]PX^_]|[\x{90}\x{98}\x{9d}\x{9e}\x{9f}])[^\x{07}\x{1b}\x{9c}]*(?:\x{07}|\x{1b}\\|\x{9c})?`)
	// Remaining two-byte escape sequences (ESC c, ESC 7...).
	escPairRE = regexp.MustCompile(`\x{1b}[ -~]?`)
	// Line breaks and tabs become a space.
	whitespaceCtrlRE = regexp.MustCompile(`[\n\r\t\v\f]`)
	// C0, DEL, C1 and bidi controls (which can spoof the text order in a terminal).
	otherCtrlRE = regexp.MustCompile(`[\x{0}-\x{1f}\x{7f}-\x{9f}\x{200e}\x{200f}\x{202a}-\x{202e}\x{2066}-\x{2069}]`)
	spacesRE    = regexp.MustCompile(`[\s\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+`)
)

// DefaultMaxLength is the default limit of Sanitize, in code points.
const DefaultMaxLength = 200

func isSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// Sanitize makes transcript text safe to show in a terminal: it removes ANSI/ESC sequences and
// control characters, turns line breaks into spaces, collapses spaces and truncates with an
// ellipsis, counting code points.
func Sanitize(text string, maxLength int) string {
	s := stringSeqRE.ReplaceAllString(text, "")
	s = csiRE.ReplaceAllString(s, "")
	s = escPairRE.ReplaceAllString(s, "")
	s = whitespaceCtrlRE.ReplaceAllString(s, " ")
	s = otherCtrlRE.ReplaceAllString(s, "")
	s = spacesRE.ReplaceAllString(s, " ")
	s = strings.TrimFunc(s, isSpace)
	runes := []rune(s)
	if len(runes) <= maxLength {
		return s
	}
	if maxLength <= 0 {
		return ""
	}
	return strings.TrimRightFunc(string(runes[:maxLength-1]), isSpace) + "…"
}
