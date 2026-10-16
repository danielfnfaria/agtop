package store

import (
	"reflect"

	"github.com/danielfnfaria/agtop/internal/domain"
)

// Discovery scans: one at a time (they run on the actor), rescheduled after each one ends; each
// scan syncs the live sessions, marks the ones that left as stale and removes them after
// StaleMs.

func (s *Store) removeSession(e *sessionEntry) {
	e.removed = true
	delete(s.sessions, e.key)
	h := domain.PruneActivity(e.state.ActivityHistogram, activityWindowStart(s.deps.Clock.Now()))
	if len(h) > 0 {
		s.retired[e.key] = retiredActivity{label: e.profile.Label, histogram: h}
	}
	s.setSubagentsDir(e, "")
	s.closeSessionWatcher(e)
	s.source.ForgetSession(e.profile, e.sessionID)
	s.closeTailers(e)
	s.changed()
}

func (s *Store) scanOnce() {
	result, err := s.source.Discover(s.opts.Profiles)
	if err != nil {
		s.pushError("discovery: " + errorCode(err))
		return
	}
	if s.isStopped() {
		return
	}
	s.lastDiscovery = &result
	s.usage.refreshAll()
	if s.isStopped() {
		return
	}
	now := s.deps.Clock.Now()
	// scannedAt alone changing does not justify a new snapshot. The usage view takes part: it
	// turns stale with time alone, and the next scan shows it.
	dirty := false
	statuses := profileStatuses(s.opts.Profiles, s.source.Provider(), s.lastDiscovery, s.usage.views(now))
	if s.profilesSig == nil || !reflect.DeepEqual(statuses, s.profilesSig) {
		s.profilesSig = statuses
		dirty = true
	}
	live := map[string]bool{}
	var tasks []*sessionEntry
	for _, pd := range result.Profiles {
		// A dead "duplicate" has the key of a live one: not handled here.
		for _, d := range pd.Live {
			live[d.Key] = true
			e := s.sessions[d.Key]
			if e == nil {
				e = newEntry(d, now, s.source.Provider())
				s.sessions[d.Key] = e
				// The session may be coming back (e.g. --resume): retired only goes at the end of
				// its first rebuild, when the transcript read again already has that activity.
				dirty = true
			} else {
				if discoveredChanged(e.discovered, d) {
					e.discovered = d
					e.discoveredVer++
					dirty = true
				}
				if e.liveness == Stale {
					e.liveness = Alive
					e.staleSince = 0
					dirty = true
				}
			}
			tasks = append(tasks, e)
		}
	}
	for _, e := range s.sessions {
		if live[e.key] {
			continue
		}
		if e.liveness == Alive {
			e.liveness = Stale
			e.staleSince = now
			dirty = true
		} else if now-e.staleSince >= s.opts.StaleMs {
			s.removeSession(e)
		}
	}
	for _, e := range tasks {
		s.runTask(e, func() error { return s.syncSession(e) })
	}
	if dirty {
		s.changed()
	}
}

// triggerScan scans now and schedules the next scan IntervalMs after this one ends.
func (s *Store) triggerScan() {
	if s.isStopped() {
		return
	}
	s.cancelScanTimer()
	s.scanOnce()
	if s.isStopped() {
		return
	}
	tok := new(struct{})
	s.scanTok = tok
	s.scanStop = s.deps.Clock.AfterFunc(s.opts.DiscoveryIntervalMs, func() {
		s.post(func() {
			if s.scanTok == tok {
				s.scanTok, s.scanStop = nil, nil
				s.triggerScan()
			}
		})
	})
}

// requestScan asks for a scan once the messages already queued are handled (a burst of
// registry events makes one scan).
func (s *Store) requestScan() {
	if !s.isStopped() {
		s.scanQueued = true
	}
}

func (s *Store) cancelScanTimer() {
	if s.scanStop != nil {
		s.scanStop()
	}
	s.scanTok, s.scanStop = nil, nil
}
