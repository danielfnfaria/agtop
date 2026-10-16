package discovery

import (
	"math"
	"os"
	"syscall"
	"testing"
)

func TestIsPidAliveSelf(t *testing.T) {
	if !IsPidAlive(os.Getpid()) {
		t.Error("own process must be alive")
	}
}

func TestPidAliveNeverKillsInvalidPID(t *testing.T) {
	kill := func(int, syscall.Signal) error {
		t.Error("kill called")
		return nil
	}
	for _, pid := range []int{0, -1, -12345, math.MaxInt32 + 1} {
		if pidAlive(kill, pid) {
			t.Errorf("pid %d alive", pid)
		}
	}
}

func TestPidAliveSignals(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"ESRCH", syscall.ESRCH, false},
		{"EPERM", syscall.EPERM, true},
		{"nil", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPID int
			var gotSig syscall.Signal = -1
			kill := func(pid int, sig syscall.Signal) error { gotPID, gotSig = pid, sig; return tt.err }
			if got := pidAlive(kill, 123); got != tt.want {
				t.Errorf("got %v", got)
			}
			if gotPID != 123 || gotSig != 0 {
				t.Errorf("kill(%d, %d)", gotPID, gotSig)
			}
		})
	}
}

func TestSystemUptimeIsPlausible(t *testing.T) {
	if up := SystemUptimeMs(); up <= 0 {
		t.Errorf("uptime = %d", up)
	}
}
