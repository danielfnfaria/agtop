package store

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
)

// EmptySnapshot is the snapshot before the first emission.
var EmptySnapshot = &Snapshot{Sessions: []*SessionView{}, Profiles: []ProfileStatus{}, Errors: []string{}}

// discoveredChanged reports whether a field of the discovery that feeds the view or the reading
// changed; it avoids replacing the discovered session (and the view) on every scan.
func discoveredChanged(a, b discovery.DiscoveredSession) bool {
	return a.PID != b.PID ||
		a.Kind != b.Kind ||
		a.Activity != b.Activity ||
		a.RawStatus != b.RawStatus ||
		a.Name != b.Name ||
		a.Cwd != b.Cwd ||
		a.StartedAt != b.StartedAt ||
		a.Files.CustomTitle != b.Files.CustomTitle ||
		a.Files.TranscriptPath != b.Files.TranscriptPath ||
		a.Files.SubagentsDir != b.Files.SubagentsDir
}

// activityWindowStart is the first bucket of the activity window that ends at now.
func activityWindowStart(now int64) int64 {
	return domain.WindowStart(domain.ActivityBucket(now))
}

func startOf(e *sessionEntry) int64 {
	if e.discovered.StartedAt != 0 {
		return e.discovered.StartedAt
	}
	return e.firstSeenAt
}

// compareEntries is the stable session order: by start, ties by key. Nothing that changes while
// a session runs takes part, so sessions never swap places.
func compareEntries(a, b *sessionEntry) int {
	if c := cmp.Compare(startOf(a), startOf(b)); c != 0 {
		return c
	}
	return strings.Compare(a.key, b.key)
}

// profileStatuses is the status of each profile (in profiles order) from the last discovery,
// with its usage; every profile belongs to provider.
func profileStatuses(profiles []config.ProfileDir, provider domain.ProviderID, last *discovery.Result,
	usage map[string]domain.UsageView) []ProfileStatus {
	out := make([]ProfileStatus, 0, len(profiles))
	for _, profile := range profiles {
		var pd *discovery.ProfileDiscovery
		if last != nil {
			for i := range last.Profiles {
				if last.Profiles[i].Profile == profile {
					pd = &last.Profiles[i]
					break
				}
			}
		}
		st := ProfileStatus{
			Label:    profile.Label,
			Provider: provider,
			Dir:      profile.Dir,
			Source:   profile.Source,
			Issues:   map[discovery.IssueReason]int{},
		}
		if pd != nil {
			st.LiveCount = len(pd.Live)
			st.SessionsDirMissing = pd.Diagnostics.SessionsDirMissing
			st.SkippedEntries = pd.Diagnostics.SkippedEntries
			for _, issue := range pd.Diagnostics.Issues {
				st.Issues[issue.Reason]++
			}
		}
		if u, ok := usage[profile.Label]; ok {
			st.Usage = &u
		}
		out = append(out, st)
	}
	return out
}

// activityByProfile sums the present sessions and the removed ones per profile, pruned to the
// window of now. It also prunes retired in place.
func activityByProfile(profiles []config.ProfileDir, entries []*sessionEntry,
	retired map[string]retiredActivity, now int64) []domain.ProfileHistogram {
	minBucket := activityWindowStart(now)
	for key, r := range retired {
		h := domain.PruneActivity(r.histogram, minBucket)
		if len(h) == 0 {
			delete(retired, key)
		} else {
			retired[key] = retiredActivity{label: r.label, histogram: h}
		}
	}
	byLabel := map[string]domain.ActivityHistogram{}
	for _, p := range profiles {
		byLabel[p.Label] = domain.ActivityHistogram{}
	}
	add := func(label string, h domain.ActivityHistogram) {
		if current, ok := byLabel[label]; ok {
			byLabel[label] = domain.MergeActivity(current, h)
		}
	}
	for _, e := range entries {
		add(e.profile.Label, e.state.ActivityHistogram)
	}
	for _, r := range retired {
		add(r.label, r.histogram)
	}
	out := make([]domain.ProfileHistogram, 0, len(profiles))
	seen := map[string]bool{}
	for _, p := range profiles {
		if seen[p.Label] {
			continue
		}
		seen[p.Label] = true
		h := maps.Clone(domain.PruneActivity(byLabel[p.Label], minBucket))
		if h == nil {
			h = domain.ActivityHistogram{}
		}
		out = append(out, domain.ProfileHistogram{Label: p.Label, Histogram: h})
	}
	return out
}

// projector turns entries into views; the view of an entry is reused while its inputs are the
// same.
type projector struct {
	learner *contextLearner
}

func (p projector) project(e *sessionEntry) *SessionView {
	s := e.state
	label := e.profile.Label
	reports := p.learner.reportsFor(label)
	learned := p.learner.learnedSignature(s, label)
	liveness := string(e.liveness) + ":" + strconv.FormatInt(e.staleSince, 10)
	if c := e.view; c != nil && c.stateVer == e.stateVer && c.discoveredVer == e.discoveredVer &&
		c.liveness == liveness && c.learned == learned {
		return c.v
	}
	d := e.discovered
	id := modelID(s)
	usage := domain.ComputeContextUsage(id, s.Context.Used, p.learner.learned, domain.ReportedWindows{
		Session: reports.ForSession(s.SessionID, id),
		Model:   reports.ForModel(id),
	})
	agents := make(map[string]*AgentView, len(s.Agents))
	for key, agent := range s.Agents {
		agents[key] = agentView(agent, p.learner.learned, reports.ForModel(agent.Model))
	}
	var model *domain.ModelInfo
	if s.Model != nil {
		m := *s.Model
		model = &m
	}
	prs := slices.Clone(s.PullRequests)
	if prs == nil {
		prs = []domain.PullRequestRef{}
	}
	v := &SessionView{
		Key:            e.key,
		SessionID:      s.SessionID,
		Provider:       e.provider,
		ProfileLabel:   label,
		PID:            d.PID,
		Kind:           d.Kind,
		Activity:       d.Activity,
		RawStatus:      d.RawStatus,
		Liveness:       e.liveness,
		StaleSince:     e.staleSince,
		StartedAt:      d.StartedAt,
		DisplayTitle:   displayTitle(e, s),
		Cwd:            s.Cwd,
		ProjectName:    s.ProjectName,
		GitBranch:      s.GitBranch,
		Version:        s.Version,
		Model:          model,
		Effort:         s.Effort,
		ContextUsed:    s.Context.Used,
		ContextWindow:  usage.ContextWindow,
		ContextPercent: usage.Percent,
		Totals:         s.Totals,
		PendingTools:   slices.Clone(s.PendingTools),
		LastActivityAt: s.LastActivityAt,
		PRs:            prs,
		Agents:         agents,
		ParseErrors:    s.ParseErrors,
	}
	e.view = &cachedView{stateVer: e.stateVer, discoveredVer: e.discoveredVer, liveness: liveness, learned: learned, v: v}
	return v
}

func displayTitle(e *sessionEntry, s domain.SessionState) string {
	d := e.discovered
	if d.Files.CustomTitle != "" {
		return d.Files.CustomTitle
	}
	if s.CustomTitle != "" {
		return s.CustomTitle
	}
	if s.AITitle != "" {
		return s.AITitle
	}
	if d.Name != "" {
		return d.Name
	}
	cwd := d.Cwd
	if cwd == "" {
		cwd = s.Cwd
	}
	if cwd != "" {
		if seg, ok := domain.LastPathSegment(cwd); ok {
			return seg
		}
	}
	if s.ProjectName != "" {
		return s.ProjectName
	}
	return e.sessionID
}

type snapshotInput struct {
	profiles []config.ProfileDir
	provider domain.ProviderID
	entries  []*sessionEntry
	last     *discovery.Result
	errors   []string
	retired  map[string]retiredActivity
	now      int64
	usage    map[string]domain.UsageView
	learner  *contextLearner
}

func buildSnapshot(in snapshotInput) *Snapshot {
	entries := slices.Clone(in.entries)
	// Learn before building any view, so that every view sees the same learning.
	for _, e := range entries {
		in.learner.learnFrom(e.state, e.profile.Label)
	}
	slices.SortFunc(entries, compareEntries)
	p := projector{in.learner}
	views := make([]*SessionView, len(entries))
	for i, e := range entries {
		views[i] = p.project(e)
	}
	var scannedAt int64
	if in.last != nil {
		scannedAt = in.last.ScannedAt
	}
	return &Snapshot{
		Sessions:          views,
		Profiles:          profileStatuses(in.profiles, in.provider, in.last, in.usage),
		ScannedAt:         scannedAt,
		Errors:            append([]string{}, in.errors...),
		ActivityByProfile: activityByProfile(in.profiles, entries, in.retired, in.now),
	}
}
