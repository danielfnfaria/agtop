//go:build linux

package proc

import (
	"reflect"
	"testing"
	"time"
)

func TestParseProcStat(t *testing.T) {
	line := "1234 (claude (worker)) S 567 1234 1234 0 -1 4194304 12345 0 0 0 123 45 0 0 20 0 12 0 987654 123456789 12345 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0"
	got, err := parseProcStat(line)
	if err != nil {
		t.Fatalf("parse = %v", err)
	}
	want := procStat{pid: 1234, comm: "claude (worker)", ppid: 567, startTicks: 987654}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseProcStatRejectsMalformed(t *testing.T) {
	for _, line := range []string{
		"",
		"no parens 123",
		"123 (comm",        // no closing paren
		"abc (comm) S 1",   // bad pid
		"123 (comm) S 1 2", // too few fields
	} {
		if _, err := parseProcStat(line); err == nil {
			t.Errorf("parseProcStat(%q) = nil error", line)
		}
	}
}

func TestParseProcStatusUID(t *testing.T) {
	text := "Name:\tclaude\nUmask:\t0022\nUid:\t1000\t1000\t1000\t1000\nGid:\t1000\t1000\t1000\t1000\n"
	got, err := parseProcStatusUID(text)
	if err != nil || got != 1000 {
		t.Errorf("uid = %d, err = %v", got, err)
	}
	for _, bad := range []struct {
		name, text string
	}{
		{"missing uid", "Name:\tclaude\n"},
		{"empty uid", "Name:\tclaude\nUid:\t\n"},
		{"invalid uid", "Name:\tclaude\nUid:\tabc\t1000\t1000\t1000\n"},
	} {
		if _, err := parseProcStatusUID(bad.text); err == nil {
			t.Errorf("%s: parseProcStatusUID(%q) = nil error", bad.name, bad.text)
		}
	}
}

func TestParseProcCmdline(t *testing.T) {
	in := "node\x00/opt/claude-code/cli.js\x00--dangerously-skip-permissions\x00"
	want := []string{"node", "/opt/claude-code/cli.js", "--dangerously-skip-permissions"}
	if got := parseProcCmdline(in); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := parseProcCmdline(""); got != nil {
		t.Errorf("empty = %q", got)
	}
}

func TestTicksToTimeKeepsPrecision(t *testing.T) {
	// 100 ticks per second: tick 33 is 330ms past the second, not truncated to the second.
	got := ticksToTime(1_700_000_000, 33)
	want := time.Unix(1_700_000_000, 330_000_000).UTC()
	if !got.Equal(want) {
		t.Errorf("ticksToTime = %v, want %v", got, want)
	}
}
