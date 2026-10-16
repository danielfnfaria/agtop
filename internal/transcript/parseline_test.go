package transcript

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseLine(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		want    any
		wantErr error
	}{
		{"valid JSON", `{"type":"user"}`, map[string]any{"type": "user"}, nil},
		{"blank line is empty", "   ", nil, ErrEmptyLine},
		{"invalid JSON does not panic nor pass the content on", `{"type": "assistant", SECRET`, nil, ErrInvalidJSON},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseLine(c.line)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), "SECRET") {
				t.Errorf("error leaks the content: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestJSNumberString(t *testing.T) {
	cases := map[float64]string{0: "0", 3: "3", -2.5: "-2.5", 1e21: "1e+21", 1e-7: "1e-7", 123456789: "123456789"}
	for in, want := range cases {
		if got := jsNumberString(in); got != want {
			t.Errorf("jsNumberString(%v) = %q, want %q", in, got, want)
		}
	}
}
