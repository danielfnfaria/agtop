package discovery

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	esc = "\x1b"
	bel = "\x07"
	rlo = "\u202e"

	// Synthetic clock: boot at 1_699_999_500_000, before registryEntry's startedAt.
	clockNow    = int64(1_700_000_500_000)
	clockUptime = int64(1_000_000)
	clockBoot   = clockNow - clockUptime
)

func clockDeps(fs FS, alive func(int) bool) Deps {
	return Deps{
		FS:         fs,
		IsPidAlive: alive,
		Now:        func() int64 { return clockNow },
		UptimeMs:   func() int64 { return clockUptime },
	}
}

func always(v bool) func(int) bool { return func(int) bool { return v } }

func TestReadSessionRegistryValidRecord(t *testing.T) {
	profile := makeProfile(t, makeTmp(t), "claude")
	writeJSON(t, filepath.Join(profile.Dir, "sessions", "4242.json"), registryEntry(4242, "sess-1_A", map[string]any{
		"status": "busy",
		"name":   esc + "[31mred" + esc + "[0m\nname" + rlo,
		"cwd":    "/tmp/x" + esc + "]0;title" + bel + "y",
	}))
	var asked []int
	deps := clockDeps(OSFS{}, func(pid int) bool { asked = append(asked, pid); return true })

	res := ReadSessionRegistry(profile, deps)

	if res.Diagnostics.SessionsDirMissing || res.Diagnostics.SkippedEntries != 0 || len(res.Diagnostics.Issues) != 0 {
		t.Errorf("diagnostics = %+v", res.Diagnostics)
	}
	want := []LiveSessionRecord{{
		Profile: profile, PID: 4242, SessionID: "sess-1_A", Cwd: "/tmp/xy", Kind: "interactive",
		Version: "9.9.9", Name: "red name", Activity: ActivityBusy, UpdatedAt: 1_700_000_100_000,
		StartedAt: 1_700_000_000_000, Alive: true,
	}}
	if !reflect.DeepEqual(res.Records, want) {
		t.Errorf("records = %+v\nwant %+v", res.Records, want)
	}
	if !reflect.DeepEqual(asked, []int{4242}) {
		t.Errorf("isPidAlive calls = %v", asked)
	}
}

func TestReadSessionRegistryUnknownStatusAndDeadPid(t *testing.T) {
	profile := makeProfile(t, makeTmp(t), "claude")
	writeJSON(t, filepath.Join(profile.Dir, "sessions", "7.json"),
		map[string]any{"pid": 7, "sessionId": "s7", "status": "weird", "updatedAt": "nope"})
	res := ReadSessionRegistry(profile, clockDeps(OSFS{}, always(false)))
	want := []LiveSessionRecord{{
		Profile: profile, PID: 7, SessionID: "s7", Activity: ActivityUnknown, RawStatus: "weird",
		DeadReason: DeadPid,
	}}
	if !reflect.DeepEqual(res.Records, want) {
		t.Errorf("records = %+v", res.Records)
	}
}

func TestReadSessionRegistryStatuses(t *testing.T) {
	profile := makeProfile(t, makeTmp(t), "claude")
	sessions := filepath.Join(profile.Dir, "sessions")
	statuses := []any{"busy", "shell", "waiting", "idle", esc + "[31mreq\nuires_action_x", 42.0}
	for i, status := range statuses {
		writeJSON(t, filepath.Join(sessions, fmt.Sprintf("%d.json", i+1)),
			registryEntry(i+1, fmt.Sprintf("s%d", i+1), map[string]any{"status": status}))
	}
	res := ReadSessionRegistry(profile, clockDeps(OSFS{}, always(true)))
	type pair struct {
		a Activity
		r string
	}
	var got []pair
	for _, r := range res.Records {
		got = append(got, pair{r.Activity, r.RawStatus})
	}
	want := []pair{
		{ActivityBusy, ""}, {ActivityShell, ""}, {ActivityWaiting, ""}, {ActivityIdle, ""},
		{ActivityUnknown, "req uires_actio…"}, {ActivityUnknown, ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestReadSessionRegistryPreBoot(t *testing.T) {
	profile := makeProfile(t, makeTmp(t), "claude")
	sessions := filepath.Join(profile.Dir, "sessions")
	writeJSON(t, filepath.Join(sessions, "1.json"), registryEntry(1, "old", map[string]any{"startedAt": clockBoot - 60_000}))
	// Inside the 5 s slack against the clock: the pid still decides.
	writeJSON(t, filepath.Join(sessions, "2.json"), registryEntry(2, "edge", map[string]any{"startedAt": clockBoot - 4_000}))
	writeJSON(t, filepath.Join(sessions, "3.json"), registryEntry(3, "new", map[string]any{"startedAt": clockBoot + 1}))
	var asked []int
	res := ReadSessionRegistry(profile, clockDeps(OSFS{}, func(pid int) bool { asked = append(asked, pid); return true }))

	type row struct {
		id    string
		alive bool
		dead  DeadReason
	}
	var got []row
	for _, r := range res.Records {
		got = append(got, row{r.SessionID, r.Alive, r.DeadReason})
	}
	want := []row{{"old", false, DeadPreBoot}, {"edge", true, ""}, {"new", true, ""}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
	if !reflect.DeepEqual(asked, []int{2, 3}) {
		t.Errorf("isPidAlive calls = %v", asked)
	}
}

func TestReadSessionRegistryNoStartedAt(t *testing.T) {
	profile := makeProfile(t, makeTmp(t), "claude")
	sessions := filepath.Join(profile.Dir, "sessions")
	noStart := registryEntry(1, "a", nil)
	delete(noStart, "startedAt")
	writeJSON(t, filepath.Join(sessions, "1.json"), noStart)
	b := registryEntry(2, "b", nil)
	delete(b, "startedAt")
	writeJSON(t, filepath.Join(sessions, "2.json"), b)
	deps := clockDeps(OSFS{}, func(pid int) bool { return pid == 1 })
	deps.UptimeMs = func() int64 { return 1 } // boot "now": any startedAt would precede it

	res := ReadSessionRegistry(profile, deps)
	if len(res.Records) != 2 {
		t.Fatalf("records = %d", len(res.Records))
	}
	a, bb := res.Records[0], res.Records[1]
	if !a.Alive || a.DeadReason != "" || a.StartedAt != 0 || bb.Alive || bb.DeadReason != DeadPid {
		t.Errorf("a = %+v, b = %+v", a, bb)
	}
}

func TestReadSessionRegistrySkipsNonRegistryNames(t *testing.T) {
	profile := makeProfile(t, makeTmp(t), "claude")
	sessions := filepath.Join(profile.Dir, "sessions")
	writeJSON(t, filepath.Join(sessions, "10.json"), registryEntry(10, "s10", nil))
	writeFile(t, filepath.Join(sessions, "10.abcdef0123.key"), "synthetic-key")
	writeJSON(t, filepath.Join(sessions, "abc.json"), registryEntry(11, "s11", nil))
	writeJSON(t, filepath.Join(sessions, "012.json"), registryEntry(12, "s12", nil))
	writeJSON(t, filepath.Join(sessions, "-5.json"), registryEntry(5, "s5", nil))
	writeJSON(t, filepath.Join(sessions, "1.5.json"), registryEntry(1, "s1", nil))
	rec := &recordingFS{}

	res := ReadSessionRegistry(profile, clockDeps(rec, always(true)))

	if len(res.Records) != 1 || res.Records[0].SessionID != "s10" {
		t.Errorf("records = %+v", res.Records)
	}
	if res.Diagnostics.SkippedEntries != 5 || len(res.Diagnostics.Issues) != 0 {
		t.Errorf("diagnostics = %+v", res.Diagnostics)
	}
	reads := rec.reads()
	if len(reads) != 1 || filepath.Base(reads[0].path) != "10.json" {
		t.Errorf("reads = %v", reads)
	}
}

func TestReadSessionRegistryInvalidRecords(t *testing.T) {
	profile := makeProfile(t, makeTmp(t), "claude")
	sessions := filepath.Join(profile.Dir, "sessions")
	const secret = "SYNTHETIC-SECRET-MARKER"
	writeFile(t, filepath.Join(sessions, "1.json"), `{ "pid": 1, "sessionId": "`+secret+`"`)
	writeJSON(t, filepath.Join(sessions, "2.json"), []string{secret})
	writeJSON(t, filepath.Join(sessions, "3.json"), map[string]any{"pid": 3, "note": secret})
	writeJSON(t, filepath.Join(sessions, "4.json"), registryEntry(-4, "s4", nil))
	writeJSON(t, filepath.Join(sessions, "5.json"), registryEntry(0, "s5", map[string]any{"pid": 5.5}))
	writeJSON(t, filepath.Join(sessions, "6.json"), registryEntry(0, "s6", nil))
	writeJSON(t, filepath.Join(sessions, "7.json"), registryEntry(7, "s7", map[string]any{"pid": "7"}))
	writeJSON(t, filepath.Join(sessions, "8.json"), registryEntry(9, "s8", nil))
	for i, sid := range []any{"../x", "/etc/passwd", `a\b`, "..", `C:\x`, "", 26} {
		n := 20 + i
		writeJSON(t, filepath.Join(sessions, fmt.Sprintf("%d.json", n)), registryEntry(n, "s", map[string]any{"sessionId": sid}))
	}
	writeFile(t, filepath.Join(sessions, "27.json"), "null")
	writeFile(t, filepath.Join(sessions, "28.json"), strings.Repeat(" ", MaxRegistryFileBytes+1))
	rec := &recordingFS{}
	deps := clockDeps(rec, func(int) bool { t.Error("isPidAlive called"); return true })

	res := ReadSessionRegistry(profile, deps)

	if len(res.Records) != 0 {
		t.Errorf("records = %+v", res.Records)
	}
	got := map[string]IssueReason{}
	for _, i := range res.Diagnostics.Issues {
		got[i.File] = i.Reason
	}
	want := map[string]IssueReason{
		"1.json": IssueInvalidJSON, "2.json": IssueNotObject, "3.json": IssueMissingFields,
		"4.json": IssueInvalidPID, "5.json": IssueInvalidPID, "6.json": IssueInvalidPID,
		"7.json": IssueInvalidPID, "8.json": IssuePIDMismatch,
		"20.json": IssueInvalidSessionID, "21.json": IssueInvalidSessionID, "22.json": IssueInvalidSessionID,
		"23.json": IssueInvalidSessionID, "24.json": IssueInvalidSessionID, "25.json": IssueInvalidSessionID,
		"26.json": IssueInvalidSessionID, "27.json": IssueNotObject, "28.json": IssueTooLarge,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("issues = %v", got)
	}
	if b, _ := json.Marshal(res.Diagnostics); strings.Contains(string(b), secret) {
		t.Error("diagnostics leak file content")
	}
	for _, c := range rec.calls {
		if !strings.HasPrefix(c.path, sessions) {
			t.Errorf("touched %s outside sessions/", c.path)
		}
	}
}

func TestReadSessionRegistryMissingDir(t *testing.T) {
	profile := makeProfile(t, makeTmp(t), "claude")
	res := ReadSessionRegistry(profile, clockDeps(OSFS{}, always(true)))
	if len(res.Records) != 0 || !res.Diagnostics.SessionsDirMissing || res.Diagnostics.SkippedEntries != 0 ||
		len(res.Diagnostics.Issues) != 0 {
		t.Errorf("result = %+v", res)
	}
}
