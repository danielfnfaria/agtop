package store

import (
	"reflect"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
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
	if src := s.sourceOf(e.provider); src != nil {
		src.ForgetSession(e.profile, e.sessionID)
	}
	s.closeTailers(e)
	s.changed()
}

// discover scans the profiles of each source with it, in the order of the sources, and joins the
// results. Profiles whose provider has no source are not scanned. A source that fails does not
// stop the others: its error is reported (once per code) and its profiles keep the result it
// returned with the error, if any, else its last good one.
func (s *Store) discover() discovery.Result {
	var out discovery.Result
	for _, src := range s.sources {
		var group []config.ProfileDir
		for _, p := range s.opts.Profiles {
			if profileProvider(p) == src.Provider() {
				group = append(group, p)
			}
		}
		if len(group) == 0 {
			continue
		}
		provider := src.Provider()
		res, err := src.Discover(group)
		if err != nil {
			code := errorCode(err)
			if s.lastScanCode[provider] != code {
				s.lastScanCode[provider] = code
				s.pushError("discovery " + string(provider) + ": " + code)
			}
			if len(res.Profiles) == 0 {
				res.Profiles = s.lastGood[provider]
			}
		} else {
			s.lastScanCode[provider] = ""
			s.lastGood[provider] = res.Profiles
		}
		if out.ScannedAt == 0 && res.ScannedAt != 0 {
			out.ScannedAt = res.ScannedAt
		}
		out.Profiles = append(out.Profiles, res.Profiles...)
	}
	s.reportUnsupported()
	if out.ScannedAt == 0 {
		// No profile was scanned (all of them lack a source, or every source failed): the scan
		// still happened.
		out.ScannedAt = s.deps.Clock.Now()
	}
	return out
}

// reportUnsupported pushes one error, the first time, naming the profiles whose provider has no
// source: they are never scanned.
func (s *Store) reportUnsupported() {
	if s.unsupportedReported {
		return
	}
	s.unsupportedReported = true
	for _, p := range s.opts.Profiles {
		if s.sourceOf(profileProvider(p)) == nil {
			s.pushError("profile " + p.Label + ": " + domain.ProviderInfo(profileProvider(p)).DisplayName + " support is not available yet")
		}
	}
}

func (s *Store) scanOnce() {
	result := s.discover()
	if s.isStopped() {
		return
	}
	s.lastDiscovery = &result
	s.usage.refreshAll()
	if s.isStopped() {
		return
	}
	s.rewatchGlobals()
	now := s.deps.Clock.Now()
	// scannedAt alone changing does not justify a new snapshot. The usage view takes part: it
	// turns stale with time alone, and the next scan shows it.
	dirty := false
	statuses := profileStatuses(s.opts.Profiles, s.lastDiscovery, s.usage.views(now))
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
				e = newEntry(d, now, profileProvider(d.Profile))
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
