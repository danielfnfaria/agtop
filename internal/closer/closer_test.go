package closer

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/danielfnfaria/agtop/internal/proc"
)

const (
	testUID   = 1000
	testStart = 1_700_000_000_000
)

func card(mut ...func(*Card)) Card {
	c := Card{
		ProfileDir: "/tmp/claude",
		PID:        4242,
		SessionID:  "sess-1",
		StartedAt:  testStart,
		Kind:       "interactive",
	}
	for _, m := range mut {
		m(&c)
	}
	return c
}

func registry(c Card) string {
	m := map[string]any{
		"pid":       float64(c.PID),
		"sessionId": c.SessionID,
		"startedAt": float64(c.StartedAt),
		"kind":      c.Kind,
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// fakeFS serves sessions/<pid>.json; a missing file returns the gone error.
type fakeFS struct {
	files map[int]string
	fail  error
}

func (f fakeFS) ReadFile(path string) (string, error) {
	if f.fail != nil {
		return "", f.fail
	}
	for pid, text := range f.files {
		if strings.HasSuffix(path, "/sessions/"+strconv.Itoa(pid)+".json") {
			return text, nil
		}
	}
	return "", errors.New("no such file")
}

// fakeProc serves process info per pid; a missing pid is not alive.
type fakeProc struct {
	infos map[int]proc.Info
}

func (f fakeProc) Lookup(pid int) (proc.Info, error) {
	if info, ok := f.infos[pid]; ok {
		return info, nil
	}
	return proc.Info{}, proc.ErrNotFound
}

// sent records the signals a signaller received.
type sent struct {
	pid int
	sig syscall.Signal
}

// fakeSignal records the signals it was asked to send and returns a canned error. It stands in
// for proc.Signaler, which revalidates the identity before signaling (see internal/proc).
type fakeSignal struct {
	sent []sent
	want proc.Info
	fail error
}

func (f *fakeSignal) Signal(pid int, sig syscall.Signal, want proc.Info) error {
	f.sent = append(f.sent, sent{pid, sig})
	f.want = want
	return f.fail
}

type deps struct {
	fs   fakeFS
	proc fakeProc
	sig  fakeSignal
	self Process
}

func (d *deps) closer() *Closer {
	return New(Deps{FS: d.fs, Proc: d.proc, Signal: &d.sig, Self: d.self})
}

// validDeps returns deps where every check passes for the given card.
func validDeps(c Card) *deps {
	d := &deps{
		self: Process{PID: 1_000_000, PPID: 999_999, UID: testUID},
	}
	d.fs = fakeFS{files: map[int]string{c.PID: registry(c)}}
	d.proc = fakeProc{infos: map[int]proc.Info{
		c.PID: {PID: c.PID, Name: "claude", Owner: testUID, Start: time.UnixMilli(c.StartedAt).UTC()},
	}}
	return d
}

func TestCloseSendsSigtermToTheRecordPid(t *testing.T) {
	for _, kind := range []string{"interactive", "background"} {
		c := card(func(c *Card) { c.Kind = kind })
		d := validDeps(c)
		if err := d.closer().Close(c); err != nil {
			t.Fatalf("%s: Close = %v", kind, err)
		}
		if len(d.sig.sent) != 1 || d.sig.sent[0] != (sent{c.PID, syscall.SIGTERM}) {
			t.Errorf("%s: sent = %+v", kind, d.sig.sent)
		}
		if d.sig.want.Start != time.UnixMilli(c.StartedAt).UTC() {
			t.Errorf("%s: signaled identity start = %v", kind, d.sig.want.Start)
		}
	}
}

func TestCloseNeverSignalsOnError(t *testing.T) {
	tests := []struct {
		name string
		card func(*Card)
		deps func(c Card) *deps
		want error
	}{
		{"pid zero", func(c *Card) { c.PID = 0 }, validDeps, ErrInvalidPID},
		{"pid one", func(c *Card) { c.PID = 1 }, validDeps, ErrInvalidPID},
		{"pid negative", func(c *Card) { c.PID = -5 }, validDeps, ErrInvalidPID},
		{"agtop itself", nil, func(c Card) *deps {
			d := validDeps(c)
			d.self.PID = c.PID
			return d
		}, ErrSelfOrParent},
		{"agtop parent", nil, func(c Card) *deps {
			d := validDeps(c)
			d.self.PPID = c.PID
			return d
		}, ErrSelfOrParent},
		{"record gone", nil, func(c Card) *deps {
			d := validDeps(c)
			delete(d.fs.files, c.PID)
			return d
		}, ErrRecordGone},
		{"record read error", nil, func(c Card) *deps {
			d := validDeps(c)
			d.fs.fail = errors.New("EIO")
			return d
		}, ErrRecordGone},
		{"record sessionId changed", nil, func(c Card) *deps {
			d := validDeps(c)
			d.fs.files[c.PID] = registry(card(func(rc *Card) { rc.SessionID = "other" }))
			return d
		}, ErrRecordChanged},
		{"record startedAt changed", nil, func(c Card) *deps {
			d := validDeps(c)
			d.fs.files[c.PID] = registry(card(func(rc *Card) { rc.StartedAt = testStart + 1 }))
			return d
		}, ErrRecordChanged},
		{"record startedAt absent", nil, func(c Card) *deps {
			d := validDeps(c)
			d.fs.files[c.PID] = `{"pid":4242,"sessionId":"sess-1","kind":"interactive"}`
			return d
		}, ErrStartMismatch},
		{"record pid changed", nil, func(c Card) *deps {
			d := validDeps(c)
			d.fs.files[c.PID] = registry(card(func(rc *Card) { rc.PID = c.PID + 1 }))
			return d
		}, ErrRecordChanged},
		{"invalid record json", nil, func(c Card) *deps {
			d := validDeps(c)
			d.fs.files[c.PID] = "not json"
			return d
		}, ErrRecordChanged},
		{"process not alive", nil, func(c Card) *deps {
			d := validDeps(c)
			delete(d.proc.infos, c.PID)
			return d
		}, ErrNotAlive},
		{"not the owner", nil, func(c Card) *deps {
			d := validDeps(c)
			d.proc.infos[c.PID] = proc.Info{PID: c.PID, Name: "claude", Owner: 2000, Start: time.UnixMilli(testStart).UTC()}
			return d
		}, ErrNotOwner},
		{"not claude", nil, func(c Card) *deps {
			d := validDeps(c)
			d.proc.infos[c.PID] = proc.Info{PID: c.PID, Name: "python3", Owner: testUID, Start: time.UnixMilli(testStart).UTC()}
			return d
		}, ErrNotClaude},
		{"node without claude-code", nil, func(c Card) *deps {
			d := validDeps(c)
			d.proc.infos[c.PID] = proc.Info{PID: c.PID, Name: "node", Args: []string{"node", "server.js"}, Owner: testUID, Start: time.UnixMilli(testStart).UTC()}
			return d
		}, ErrNotClaude},
		{"start too far", nil, func(c Card) *deps {
			d := validDeps(c)
			d.proc.infos[c.PID] = proc.Info{PID: c.PID, Name: "claude", Owner: testUID, Start: time.UnixMilli(testStart + 20_000).UTC()}
			return d
		}, ErrStartMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := card()
			if tt.card != nil {
				tt.card(&c)
			}
			d := tt.deps(c)
			err := d.closer().Close(c)
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("Close = %v, want %v", err, tt.want)
			}
			if err == nil {
				t.Fatal("expected an error")
			}
			if len(d.sig.sent) != 0 {
				t.Errorf("a signal was sent on error: %+v", d.sig.sent)
			}
		})
	}
}

func TestClosePropagatesSignalError(t *testing.T) {
	c := card()
	d := validDeps(c)
	d.sig.fail = errors.New("EPERM")
	err := d.closer().Close(c)
	if err == nil {
		t.Fatal("Close = nil, want an error")
	}
	if len(d.sig.sent) != 1 || d.sig.sent[0].sig != syscall.SIGTERM {
		t.Errorf("sent = %+v", d.sig.sent)
	}
}

func TestCloseAcceptsNodeRunningClaudeCode(t *testing.T) {
	c := card()
	d := validDeps(c)
	d.proc.infos[c.PID] = proc.Info{
		PID: c.PID, Name: "node", Owner: testUID,
		Args:  []string{"node", "/opt/claude-code/cli.js", "--dangerously-skip-permissions"},
		Start: time.UnixMilli(testStart).UTC(),
	}
	if err := d.closer().Close(c); err != nil {
		t.Fatalf("Close = %v", err)
	}
	if len(d.sig.sent) != 1 || d.sig.sent[0].sig != syscall.SIGTERM {
		t.Errorf("sent = %+v", d.sig.sent)
	}
}

func TestCloseRejectsIncidentalClaudeCodeArg(t *testing.T) {
	// "claude-code" as a flag value or a directory in another argument must not be accepted; only
	// the structural entrypoint (cli.js inside a claude-code directory) is.
	c := card()
	for name, args := range map[string][]string{
		"flag value":   {"node", "server.js", "--log=/tmp/claude-code.log"},
		"other script": {"node", "/opt/claude-code/helper.js"},
		"elsewhere":    {"node", "/usr/lib/node_modules/cli.js"},
	} {
		t.Run(name, func(t *testing.T) {
			d := validDeps(c)
			d.proc.infos[c.PID] = proc.Info{
				PID: c.PID, Name: "node", Args: args, Owner: testUID,
				Start: time.UnixMilli(testStart).UTC(),
			}
			err := d.closer().Close(c)
			if !errors.Is(err, ErrNotClaude) {
				t.Fatalf("Close = %v, want ErrNotClaude", err)
			}
			if len(d.sig.sent) != 0 {
				t.Errorf("sent = %+v", d.sig.sent)
			}
		})
	}
}

func TestCloseFailsClosedOnUnknownStart(t *testing.T) {
	// A zero process start or a zero registry startedAt leaves no way to detect a reused pid, so
	// Close fails closed instead of signaling.
	for name, mut := range map[string]func(*Card){
		"proc start unknown": func(c *Card) {},
		"startedAt absent":   func(c *Card) { c.StartedAt = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			c := card(mut)
			d := validDeps(c)
			if name == "proc start unknown" {
				d.proc.infos[c.PID] = proc.Info{PID: c.PID, Name: "claude", Owner: testUID}
			}
			err := d.closer().Close(c)
			if !errors.Is(err, ErrStartMismatch) {
				t.Fatalf("Close = %v, want ErrStartMismatch", err)
			}
			if len(d.sig.sent) != 0 {
				t.Errorf("sent = %+v", d.sig.sent)
			}
		})
	}
}

func TestCloseSignalsOnlySIGTERM(t *testing.T) {
	c := card()
	d := validDeps(c)
	if err := d.closer().Close(c); err != nil {
		t.Fatalf("Close = %v", err)
	}
	if d.sig.sent[0].sig != syscall.SIGTERM {
		t.Errorf("sig = %v, want SIGTERM", d.sig.sent[0].sig)
	}
}
