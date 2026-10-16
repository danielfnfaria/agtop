package domain

import "testing"

func TestSanitize(t *testing.T) {
	rlo, isolate, emoji := "\u202e", "\u2066", "\U0001F600"
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"clear screen CSI", "antes\x1b[2Jdepois", 200, "antesdepois"},
		{"ANSI colors", "\x1b[1;31mvermelho\x1b[0m", 200, "vermelho"},
		{"CR LF TAB become a space and collapse", "a\rb\n\nc\td", 200, "a b c d"},
		{"C1 CSI with params", "x\u009b2Jy", 200, "xy"},
		{"C1 CSI loose", "x\u009b", 200, "x"},
		{"OSC window title", "a\x1b]0;titulo\x07b", 200, "ab"},
		{"OSC hyperlink", "a\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\b", 200, "alinkb"},
		{"other controls, DEL and bidi", "a\x00b\x07c\x7fd\u0085e" + rlo + "f" + isolate + "g\x1b", 200, "abcdefg"},
		{"truncates with ellipsis", "abcdefghij", 5, "abcd…"},
		{"short text untouched", "abc", 5, "abc"},
		{"does not split code points", emoji + emoji + emoji, 2, emoji + "…"},
		{"zero max", "abc", 0, ""},
		{"trims the ends", "  \n oi \t ", 200, "oi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Sanitize(tt.in, tt.max); got != tt.want {
				t.Errorf("Sanitize(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
			}
		})
	}
}
