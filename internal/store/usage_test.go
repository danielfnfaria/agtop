package store

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/ingest/watcher"
)

func usagePath(tr tree) string { return filepath.Join(tr.profile.Dir, "agtop", "usage.json") }

// writeAtomic writes like the hook: a new file renamed over the old one.
func writeAtomic(t *testing.T, p, content string) {
	t.Helper()
	mkdirAll(t, filepath.Dir(p))
	tmp := p + ".tmp"
	writeFile(t, tmp, content)
	// Same-size rewrites inside one mtime tick (about 4ms on Linux) look unchanged to the
	// size:mtime signature, so give every write a distinct, increasing mtime.
	mt := time.Unix(1_700_000_000, 0).Add(time.Duration(atomic.AddInt64(&usageWrites, 1)) * time.Second)
	if err := os.Chtimes(tmp, mt, mt); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, p); err != nil {
		t.Fatal(err)
	}
}

var usageWrites int64

func writeUsage(t *testing.T, tr tree, updatedAt int64, percent float64, resetsAt int64) {
	t.Helper()
	writeAtomic(t, usagePath(tr), jsonOf(obj{
		"version": 1, "updatedAt": updatedAt,
		"fiveHour": obj{"percent": percent, "resetsAt": resetsAt},
		"sevenDay": obj{"percent": 23},
	}))
}

func usageOf(s *Snapshot) *domain.UsageView {
	if len(s.Profiles) == 0 {
		return nil
	}
	return s.Profiles[0].Usage
}

func fiveHourPercent(s *Snapshot) float64 {
	u := usageOf(s)
	if u == nil || u.FiveHour == nil || u.FiveHour.Percent == nil {
		return -1
	}
	return *u.FiveHour.Percent
}

func withClock(c *fakeClock) Deps { return Deps{Clock: c} }

func TestPlanUsage(t *testing.T) {
	t.Run("read on the scan; absent without a file", func(t *testing.T) {
		clock := newFakeClock(nowMs())
		tr := makeTree(t)
		empty := newTestStore(t, tr, noWatch(DefaultDiscoveryIntervalMs), withClock(clock))
		empty.Start()
		if usageOf(empty.Snapshot()) != nil {
			t.Error("usage without a file")
		}
		empty.Stop()

		updated := clock.Now() - 1_000
		writeUsage(t, tr, updated, 79, updated+3_600_000)
		s := newTestStore(t, tr, noWatch(DefaultDiscoveryIntervalMs), withClock(clock))
		s.Start()
		pct := func(v float64) *float64 { return &v }
		want := domain.UsageView{
			FiveHour:  &domain.UsageWindowView{Percent: pct(79), ResetsAt: updated + 3_600_000},
			SevenDay:  &domain.UsageWindowView{Percent: pct(23)},
			UpdatedAt: updated,
		}
		if got := usageOf(s.Snapshot()); got == nil || !reflect.DeepEqual(*got, want) {
			t.Errorf("usage = %+v, want %+v", got, want)
		}
	})

	t.Run("a change is seen by the next scan; a removed file clears the usage", func(t *testing.T) {
		clock := newFakeClock(nowMs())
		tr := makeTree(t)
		writeUsage(t, tr, clock.Now(), 79, clock.Now()+3_600_000)
		s := newTestStore(t, tr, noWatch(1_000), withClock(clock))
		s.Start()
		if got := fiveHourPercent(s.Snapshot()); got != 79 {
			t.Fatalf("percent = %v", got)
		}
		writeUsage(t, tr, clock.Now(), 91, clock.Now()+3_600_000)
		s.waitFor(t, func(x *Snapshot) bool { return fiveHourPercent(x) == 91 }, 500)
		removeFile(t, usagePath(tr))
		s.waitFor(t, func(x *Snapshot) bool { return usageOf(x) == nil }, 500)
	})

	t.Run("turns stale with time alone: the next scan emits it", func(t *testing.T) {
		clock := newFakeClock(nowMs())
		tr := makeTree(t)
		// The 5h window resets in an hour: only the age makes it stale here.
		writeUsage(t, tr, clock.Now(), 50, clock.Now()+3_600_000)
		s := newTestStore(t, tr, noWatch(1_000), withClock(clock))
		s.Start()
		if u := usageOf(s.Snapshot()); u == nil || u.Stale {
			t.Fatal("stale at start")
		}
		clock.Advance(domain.UsageStaleMs)
		s.Refresh()
		if usageOf(s.Snapshot()).Stale {
			t.Fatal("stale at exactly UsageStaleMs")
		}
		snap := s.waitFor(t, func(x *Snapshot) bool { u := usageOf(x); return u != nil && u.Stale }, 1_000)
		if fiveHourPercent(snap) != 50 {
			t.Errorf("percent = %v", fiveHourPercent(snap))
		}
	})

	t.Run("a passed reset: the window percent is unknown and the usage stale", func(t *testing.T) {
		clock := newFakeClock(nowMs())
		tr := makeTree(t)
		writeUsage(t, tr, clock.Now()-2_000, 97, clock.Now()-1_000)
		s := newTestStore(t, tr, noWatch(DefaultDiscoveryIntervalMs), withClock(clock))
		s.Start()
		u := usageOf(s.Snapshot())
		if u == nil || !u.Stale || u.FiveHour == nil || u.FiveHour.Percent != nil {
			t.Errorf("usage = %+v", u)
		}
	})

	t.Run("invalid file: one short error (no content), no usage", func(t *testing.T) {
		clock := newFakeClock(nowMs())
		tr := makeTree(t)
		mkdirAll(t, filepath.Dir(usagePath(tr)))
		writeFile(t, usagePath(tr), jsonOf(obj{"version": 1, "updatedAt": 1, "cwd": "SECRET"}))
		s := newTestStore(t, tr, noWatch(1_000), withClock(clock))
		s.Start()
		s.Refresh()
		s.Refresh()
		snap := s.Snapshot()
		if usageOf(snap) != nil {
			t.Error("usage from an invalid file")
		}
		var errs []string
		for _, e := range snap.Errors {
			if strings.Contains(e, "usage") {
				errs = append(errs, e)
			}
		}
		if want := []string{"usage " + usagePath(tr) + ": INVALID"}; !slices.Equal(errs, want) {
			t.Errorf("errors = %q", errs)
		}
		if strings.Contains(jsonOf(snap), "SECRET") {
			t.Error("snapshot leaks file content")
		}
	})

	t.Run("watcher of agtop/: an event re-reads the file without waiting for a scan", func(t *testing.T) {
		clock := newFakeClock(nowMs())
		tr := makeTree(t)
		writeUsage(t, tr, clock.Now(), 79, clock.Now()+3_600_000)
		w := &fakeWatchers{}
		s := newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs = 3_600_000 }, Deps{Clock: clock, NewWatcher: w.factory})
		s.Start()
		uw := w.get(t, watcher.KindUsage)
		root, _ := filepath.EvalSymlinks(filepath.Join(tr.profile.Dir, "agtop"))
		if uw.target != (watcher.Target{Kind: watcher.KindUsage, Root: root}) {
			t.Errorf("target = %+v", uw.target)
		}
		writeUsage(t, tr, clock.Now(), 64, clock.Now()+3_600_000)
		uw.emit(watcher.Change, usagePath(tr))
		s.waitFor(t, func(x *Snapshot) bool { return fiveHourPercent(x) == 64 }, 0)
		s.Stop()
		<-s.Done()
		if !uw.isClosed() {
			t.Error("usage watcher left open")
		}
	})

	t.Run("agtop/ created after start: the scan opens its watcher", func(t *testing.T) {
		clock := newFakeClock(nowMs())
		tr := makeTree(t)
		w := &fakeWatchers{}
		s := newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs = 1_000 }, Deps{Clock: clock, NewWatcher: w.factory})
		s.Start()
		count := func() int {
			n := 0
			for _, x := range w.list() {
				if x.target.Kind == watcher.KindUsage {
					n++
				}
			}
			return n
		}
		if count() != 0 {
			t.Fatal("usage watcher before agtop/ exists")
		}
		writeUsage(t, tr, clock.Now(), 12, clock.Now()+3_600_000)
		s.waitFor(t, func(x *Snapshot) bool { return fiveHourPercent(x) == 12 }, 500)
		if count() != 1 {
			t.Errorf("usage watchers = %d", count())
		}
	})
}

func TestPlanUsageRealWatcher(t *testing.T) {
	tr := makeTree(t)
	writeUsage(t, tr, nowMs(), 10, nowMs()+3_600_000)
	s := newTestStore(t, tr, func(o *Options) { o.DiscoveryIntervalMs, o.EmitThrottleMs = 60_000, 10 }, Deps{Clock: realClock{}})
	s.Start()
	if got := fiveHourPercent(s.Snapshot()); got != 10 {
		t.Fatalf("percent = %v", got)
	}
	for attempt := 0; ; attempt++ {
		writeUsage(t, tr, nowMs(), float64(20+attempt), nowMs()+3_600_000)
		if waitReal(s, func(x *Snapshot) bool { return fiveHourPercent(x) >= 20 }, time.Second) {
			break
		}
		// The watcher may drop an event under load; a new write nudges it.
		if attempt >= 7 {
			t.Fatal("rewrite of usage.json not seen")
		}
	}
	if s.WatchedPathCount() == 0 {
		t.Error("no watched path")
	}
}

// waitReal waits (real time) until pred holds.
func waitReal(s *testStore, pred func(*Snapshot) bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred(s.Snapshot()) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return pred(s.Snapshot())
}
