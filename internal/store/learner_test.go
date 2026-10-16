package store

import (
	"maps"
	"testing"

	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/domain/reduce"
)

const (
	opus  = "claude-opus-4-5"
	haiku = "claude-haiku-4-5"
)

func learnerSession(model string, used int, agents ...domain.AgentState) domain.SessionState {
	s := reduce.NewSessionState("s1")
	if model != "" {
		s.Model = &domain.ModelInfo{ID: model}
	}
	s.Context.Used = used
	for i, a := range agents {
		s.Agents["a"+string(rune('0'+i))] = a
	}
	return s
}

func learnerAgent(model string, used int) domain.AgentState {
	a := reduce.NewAgentState()
	a.Model = model
	a.Context.Used = used
	return a
}

func TestContextLearner(t *testing.T) {
	ext := domain.ExtendedContextWindow
	tests := []struct {
		name  string
		learn []domain.SessionState
		want  domain.LearnedContextWindows
	}{
		{"usage within the table window teaches nothing",
			[]domain.SessionState{learnerSession(opus, 150_000, learnerAgent(haiku, 199_000))}, domain.LearnedContextWindows{}},
		{"session usage above the window teaches 1M for the model base",
			[]domain.SessionState{learnerSession(opus, 250_000)}, domain.LearnedContextWindows{opus: ext}},
		{"agents teach too",
			[]domain.SessionState{learnerSession(opus, 10, learnerAgent(haiku, 300_000), learnerAgent("", 900_000))},
			domain.LearnedContextWindows{haiku: ext}},
		{"learning only grows",
			[]domain.SessionState{learnerSession(opus, 250_000), learnerSession(opus, 10)}, domain.LearnedContextWindows{opus: ext}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newContextLearner(nil)
			for _, s := range tt.learn {
				l.learnFrom(s, "")
			}
			if !maps.Equal(l.learned, tt.want) {
				t.Errorf("learned = %v, want %v", l.learned, tt.want)
			}
		})
	}

	t.Run("starts empty", func(t *testing.T) {
		l := newContextLearner(nil)
		if sig := l.learnedSignature(learnerSession(opus, 10), ""); sig != "" {
			t.Errorf("signature = %q", sig)
		}
		if _, ok := l.learnedFor(opus); ok {
			t.Error("learned without data")
		}
	})

	t.Run("learnedFor ignores the suffix", func(t *testing.T) {
		l := newContextLearner(nil)
		l.learnFrom(learnerSession(opus, 250_000), "")
		cases := []struct {
			model string
			want  int
			ok    bool
		}{{opus + "[foo]", ext, true}, {haiku, 0, false}, {"", 0, false}}
		for _, c := range cases {
			if got, ok := l.learnedFor(c.model); got != c.want || ok != c.ok {
				t.Errorf("learnedFor(%q) = %d, %v", c.model, got, ok)
			}
		}
	})

	t.Run("signature covers only the models of the session and its agents, sorted", func(t *testing.T) {
		l := newContextLearner(nil)
		l.learnFrom(learnerSession(opus, 250_000), "")
		l.learnFrom(learnerSession(haiku, 250_000), "")
		cases := []struct {
			s    domain.SessionState
			want string
		}{
			{learnerSession("claude-sonnet-4-5", 10), ""},
			{learnerSession(opus, 10), opus + "=1000000"},
			{learnerSession(opus, 10, learnerAgent(haiku, 1)), haiku + "=1000000," + opus + "=1000000"},
		}
		for _, c := range cases {
			if got := l.learnedSignature(c.s, ""); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		}
	})
}

func TestContextLearnerWithReports(t *testing.T) {
	file := func(sessions map[string]domain.ContextEntry) domain.ContextReports {
		return domain.NewContextReports(domain.ContextFile{Version: 1, UpdatedAt: 1, Sessions: sessions})
	}
	reports := file(map[string]domain.ContextEntry{
		"s1":    {Model: opus, Size: 400_000, At: 1},
		"other": {Model: haiku, Size: 300_000, At: 2},
	})
	newLearner := func() *contextLearner {
		return newContextLearner(func(label string) domain.ContextReports {
			if label == "p1" {
				return reports
			}
			return file(nil)
		})
	}

	t.Run("a session with a reported window teaches nothing; neither does its model", func(t *testing.T) {
		l := newLearner()
		l.learnFrom(learnerSession(opus, 250_000), "p1")
		l.learnFrom(learnerSession(haiku, 250_000), "p1")
		if len(l.learned) != 0 {
			t.Errorf("learned = %v", l.learned)
		}
		// Another profile has no reports: the usage teaches as before.
		l.learnFrom(learnerSession(opus, 250_000), "p2")
		if got, _ := l.learnedFor(opus); got != domain.ExtendedContextWindow {
			t.Errorf("learned = %d", got)
		}
	})

	t.Run("agents only follow the reported window of their model", func(t *testing.T) {
		l := newLearner()
		l.learnFrom(learnerSession("claude-sonnet-4-5", 10, learnerAgent(haiku, 250_000)), "p1")
		if len(l.learned) != 0 {
			t.Errorf("learned = %v", l.learned)
		}
	})

	t.Run("the signature covers the reported values the session and agents use", func(t *testing.T) {
		l := newLearner()
		cases := []struct {
			s     domain.SessionState
			label string
			want  string
		}{
			{learnerSession(opus, 10), "p1", opus + ":model=400000,session=400000"},
			{learnerSession(opus, 10, learnerAgent(haiku, 1)), "p1", haiku + ":model=300000," + opus + ":model=400000,session=400000"},
			{learnerSession(opus, 10), "p2", ""},
			{learnerSession(opus, 10), "", ""},
		}
		for _, c := range cases {
			if got := l.learnedSignature(c.s, c.label); got != c.want {
				t.Errorf("label %q: got %q, want %q", c.label, got, c.want)
			}
		}
	})
}
