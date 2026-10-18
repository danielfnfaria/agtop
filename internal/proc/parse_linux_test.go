//go:build linux

package proc

import (
	"os"
	"path/filepath"
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

func TestListProc(t *testing.T) {
	root := t.TempDir()
	write := func(p, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "stat"), "cpu 1 2 3\nbtime 1700000000\n")
	cwd := filepath.Join(root, "work")
	exe := filepath.Join(root, "opencode")
	write(filepath.Join(cwd, ".keep"), "")
	write(exe, "")
	// 55813: opencode, started 100 ticks (1 s) after boot.
	write(filepath.Join(root, "55813", "comm"), "opencode\n")
	write(filepath.Join(root, "55813", "stat"), "55813 (opencode) S 1 55813 55813 0 -1 4194304 1 0 0 0 1 1 0 0 20 0 1 0 100 1 1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0")
	if err := os.Symlink(cwd, filepath.Join(root, "55813", "cwd")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(root, "55813", "exe")); err != nil {
		t.Fatal(err)
	}
	// 42: another program; "self" and "sys": not pids.
	write(filepath.Join(root, "42", "comm"), "node\n")
	write(filepath.Join(root, "self", "comm"), "opencode\n")
	got, err := listProc(root, "opencode")
	if err != nil {
		t.Fatal(err)
	}
	want := []Process{{PID: 55813, Name: "opencode", Exe: exe, Cwd: cwd, Start: time.Unix(1700000001, 0).UTC()}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if _, err := listProc(filepath.Join(root, "missing"), "opencode"); err == nil {
		t.Error("a missing root must fail")
	}
}
