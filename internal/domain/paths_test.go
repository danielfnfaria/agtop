package domain

import "testing"

func TestLastPathSegment(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/a/b/c.ts", "c.ts", true},
		{`C:\x\y.txt`, "y.txt", true},
		{"/a/b/", "b", true},
		{"plain", "plain", true},
		{"", "", false},
		{"/", "", false},
		{`\\`, "", false},
	}
	for _, tt := range tests {
		got, ok := LastPathSegment(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("LastPathSegment(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
