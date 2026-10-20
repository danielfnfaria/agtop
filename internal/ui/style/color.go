package style

import (
	"regexp"
	"slices"
	"strings"
)

// Color is a tone name (see ToneNames) or a hex color (#rgb or #rrggbb).
type Color = string

// ToneNames are the color names the config file accepts, in documentation order.
var ToneNames = []string{
	"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white", "gray",
	"blackBright", "redBright", "greenBright", "yellowBright", "blueBright",
	"magentaBright", "cyanBright", "whiteBright",
}

var hexColor = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// IsHex reports whether s is #rgb or #rrggbb.
func IsHex(s string) bool { return hexColor.MatchString(s) }

// IsTone reports whether s is one of ToneNames.
func IsTone(s string) bool {
	return slices.Contains(ToneNames, s)
}

// IsColor reports whether s is a tone name or a hex color.
func IsColor(s string) bool { return IsTone(s) || IsHex(s) }

// ANSIIndex maps a tone name to its ANSI 16-color index: black..white are 0-7, gray is an alias
// of blackBright (8), and the *Bright names are 8-15.
func ANSIIndex(name string) (int, bool) {
	if name == "gray" {
		return 8, true
	}
	bright := 0
	base := name
	if b, ok := strings.CutSuffix(name, "Bright"); ok {
		bright, base = 8, b
	}
	for i, n := range ToneNames[:8] {
		if n == base {
			return i + bright, true
		}
	}
	return 0, false
}

// ExpandHex returns #rrggbb (lowercase) for a valid hex color, expanding #rgb.
func ExpandHex(s string) (string, bool) {
	if !IsHex(s) {
		return "", false
	}
	s = strings.ToLower(s)
	if len(s) == 4 {
		return "#" + strings.Repeat(s[1:2], 2) + strings.Repeat(s[2:3], 2) + strings.Repeat(s[3:4], 2), true
	}
	return s, true
}
