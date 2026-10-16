package style

import (
	"reflect"
	"testing"
)

func TestProfileTone(t *testing.T) {
	n := len(DarkTheme.Profiles)
	tests := []struct {
		idx  int
		want string
	}{{0, "magenta"}, {1, "blue"}, {n, "magenta"}, {n + 1, "blue"}, {-1, "whiteBright"}}
	if n != 6 {
		t.Fatalf("palette has %d tones", n)
	}
	for _, tt := range tests {
		if got := ProfileTone(tt.idx, nil); got != tt.want {
			t.Errorf("ProfileTone(%d) = %q, want %q", tt.idx, got, tt.want)
		}
	}
}

func TestProfileToneMap(t *testing.T) {
	got := ProfileToneMap([]string{"claude", "claude-x"}, nil)
	if want := map[string]Color{"claude": "magenta", "claude-x": "blue"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
	if len(ProfileToneMap(nil, nil)) != 0 {
		t.Error("not empty")
	}
}
