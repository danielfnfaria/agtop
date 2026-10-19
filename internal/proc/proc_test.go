package proc

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestStartMillis(t *testing.T) {
	if got := (Info{}).StartMillis(); got != 0 {
		t.Errorf("zero start = %d", got)
	}
	at := time.UnixMilli(1_700_000_000_000).UTC()
	if got := (Info{Start: at}).StartMillis(); got != 1_700_000_000_000 {
		t.Errorf("start = %d", got)
	}
}

func TestSameStart(t *testing.T) {
	at := time.UnixMilli(1_700_000_000_000).UTC()
	if !SameStart(Info{Start: at}, Info{Start: at}) {
		t.Error("same start does not match")
	}
	if SameStart(Info{}, Info{Start: at}) {
		t.Error("unknown start matches")
	}
	if SameStart(Info{Start: at}, Info{Start: at.Add(time.Second)}) {
		t.Error("different start matches")
	}
}

func TestRevalidateAndKill(t *testing.T) {
	at := time.UnixMilli(1_700_000_000_000).UTC()
	want := Info{Start: at}
	pid, sig := 4242, syscall.SIGTERM

	t.Run("signals when the identity is unchanged", func(t *testing.T) {
		var killed []int
		lookup := func(p int) (Info, error) { return Info{Start: at}, nil }
		kill := func(p int, s syscall.Signal) error {
			killed = append(killed, p)
			if s != sig {
				t.Errorf("sig = %v", s)
			}
			return nil
		}
		if err := revalidateAndKill(pid, sig, want, lookup, kill); err != nil {
			t.Fatalf("revalidateAndKill = %v", err)
		}
		if len(killed) != 1 || killed[0] != pid {
			t.Errorf("killed = %+v", killed)
		}
	})

	t.Run("does not signal when the identity changed", func(t *testing.T) {
		var killed []int
		lookup := func(p int) (Info, error) { return Info{Start: at.Add(time.Second)}, nil }
		kill := func(p int, s syscall.Signal) error {
			killed = append(killed, p)
			return nil
		}
		err := revalidateAndKill(pid, sig, want, lookup, kill)
		if !errors.Is(err, ErrIdentityChanged) {
			t.Fatalf("err = %v, want ErrIdentityChanged", err)
		}
		if len(killed) != 0 {
			t.Errorf("kill was called on a changed identity: %+v", killed)
		}
	})

	t.Run("does not signal when the lookup fails", func(t *testing.T) {
		var killed []int
		lookup := func(p int) (Info, error) { return Info{}, ErrNotFound }
		kill := func(p int, s syscall.Signal) error {
			killed = append(killed, p)
			return nil
		}
		if err := revalidateAndKill(pid, sig, want, lookup, kill); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if len(killed) != 0 {
			t.Errorf("kill was called on a failed lookup: %+v", killed)
		}
	})
}

// TestLookupSelf reads the running process: it only observes, never signals.
func TestLookupSelf(t *testing.T) {
	info, err := Lookup(os.Getpid())
	if err != nil {
		t.Fatalf("Lookup(self) = %v", err)
	}
	if info.PID != os.Getpid() {
		t.Errorf("pid = %d, want %d", info.PID, os.Getpid())
	}
	if info.Owner != os.Getuid() {
		t.Errorf("owner = %d, want %d", info.Owner, os.Getuid())
	}
	if info.Name == "" {
		t.Error("name is empty")
	}
	if info.Start.IsZero() {
		t.Error("start is unknown for a live process")
	}
}

func TestRevalidateAndKillGoneProcess(t *testing.T) {
	at := time.UnixMilli(1_700_000_000_000).UTC()
	lookup := func(int) (Info, error) { return Info{Start: at}, nil }
	kill := func(int, syscall.Signal) error { return syscall.ESRCH }
	err := revalidateAndKill(4242, syscall.SIGTERM, Info{Start: at}, lookup, kill)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if !errors.Is(err, syscall.ESRCH) {
		t.Errorf("err = %v lost its ESRCH cause", err)
	}
}

func TestSignalExitedProcess(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	// The child was reaped: its pid names no process (a reuse in this window is negligible).
	err := OSSignal{}.Signal(cmd.ProcessState.Pid(), syscall.SIGTERM, Info{Start: time.Now()})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
