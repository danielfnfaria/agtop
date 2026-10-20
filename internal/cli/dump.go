package cli

import (
	"encoding/json"
	"io"
	"math"

	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/store"
)

// DumpSummary is what `agtop --dump` prints: metadata only (counts, sanitized titles).
type DumpSummary struct {
	ScannedAt  int64         `json:"scannedAt,omitempty"`
	Profiles   []DumpProfile `json:"profiles"`
	Totals     DumpTotals    `json:"totals"`
	Sessions   []DumpSession `json:"sessions"`
	ErrorCount int           `json:"errorCount"`
}

// DumpProfile is the per-profile discovery summary in the dump output.
type DumpProfile struct {
	Label              string                        `json:"label"`
	LiveCount          int                           `json:"liveCount"`
	SessionsDirMissing bool                          `json:"sessionsDirMissing"`
	SkippedEntries     int                           `json:"skippedEntries"`
	Issues             map[discovery.IssueReason]int `json:"issues"`
}

// DumpTotals is the aggregate counts across all sessions in the dump output.
type DumpTotals struct {
	Sessions       int                        `json:"sessions"`
	AgentsByStatus map[domain.AgentStatus]int `json:"agentsByStatus"`
}

// DumpSession is one session as printed in the dump output.
type DumpSession struct {
	Profile        string               `json:"profile"`
	Liveness       store.Liveness       `json:"liveness"`
	Activity       discovery.Activity   `json:"activity"`
	RawStatus      string               `json:"rawStatus,omitempty"`
	Kind           string               `json:"kind,omitempty"`
	Title          string               `json:"title"`
	Model          string               `json:"model,omitempty"`
	ContextWindow  domain.ContextWindow `json:"contextWindow"`
	ContextPercent float64              `json:"contextPercent"`
	PendingTools   int                  `json:"pendingTools"`
	Agents         DumpAgents           `json:"agents"`
	ParseErrors    int                  `json:"parseErrors"`
}

// DumpAgents counts a session's agents, in total and by status.
type DumpAgents struct {
	Total    int                        `json:"total"`
	ByStatus map[domain.AgentStatus]int `json:"byStatus"`
}

// Dump runs one scan without watchers and prints the JSON summary on stdout.
func Dump(profiles []config.ProfileDir, stdout io.Writer) int {
	opts := store.DefaultOptions(profiles)
	opts.Watch = false
	s := store.New(opts, store.Deps{})
	s.Start()
	snap := s.Snapshot()
	s.Stop()
	<-s.Done()
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(Summarize(snap)); err != nil {
		return 1
	}
	return 0
}

// Summarize is the dump of a snapshot.
func Summarize(snap *store.Snapshot) DumpSummary {
	out := DumpSummary{
		ScannedAt:  snap.ScannedAt,
		Profiles:   make([]DumpProfile, 0, len(snap.Profiles)),
		Totals:     DumpTotals{Sessions: len(snap.Sessions), AgentsByStatus: map[domain.AgentStatus]int{}},
		Sessions:   make([]DumpSession, 0, len(snap.Sessions)),
		ErrorCount: len(snap.Errors),
	}
	for _, p := range snap.Profiles {
		out.Profiles = append(out.Profiles, DumpProfile{
			Label:              p.Label,
			LiveCount:          p.LiveCount,
			SessionsDirMissing: p.SessionsDirMissing,
			SkippedEntries:     p.SkippedEntries,
			Issues:             p.Issues,
		})
	}
	for _, v := range snap.Sessions {
		byStatus := map[domain.AgentStatus]int{}
		for _, a := range v.Agents {
			byStatus[a.Status]++
			out.Totals.AgentsByStatus[a.Status]++
		}
		var model string
		if v.Model != nil {
			model = v.Model.ID
		}
		out.Sessions = append(out.Sessions, DumpSession{
			Profile:        v.ProfileLabel,
			Liveness:       v.Liveness,
			Activity:       v.Activity,
			RawStatus:      v.RawStatus,
			Kind:           v.Kind,
			Title:          v.DisplayTitle,
			Model:          model,
			ContextWindow:  v.ContextWindow,
			ContextPercent: math.Round(v.ContextPercent*10) / 10,
			PendingTools:   len(v.PendingTools),
			Agents:         DumpAgents{Total: len(v.Agents), ByStatus: byStatus},
			ParseErrors:    v.ParseErrors,
		})
	}
	return out
}
