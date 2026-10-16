package style

import "testing"

func TestSpinnerFrame(t *testing.T) {
	frames := len(UnicodeGlyphs.Spinner)
	tests := []struct {
		name string
		now  int64
		want int
	}{
		{"zero", 0, 0}, {"just before the next", 159, 0}, {"one frame", 160, 1},
		{"wraps", 160 * int64(frames), 0}, {"negative still valid", -1, frames - 1},
	}
	if SpinnerFrameMS != 160 {
		t.Fatal("frame duration")
	}
	for _, tt := range tests {
		if got := SpinnerFrame(tt.now, frames); got != tt.want {
			t.Errorf("%s: SpinnerFrame(%d) = %d, want %d", tt.name, tt.now, got, tt.want)
		}
	}
}
