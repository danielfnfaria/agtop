package ui

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/danielfnfaria/agtop/internal/store"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

var update = flag.Bool("update", false, "rewrite the golden files")

type scenario struct {
	name   string
	opts   func() Options
	w, h   int
	keys   []string
	colors bool
}

func fiveStore() Options {
	return testOptions(newFakeStore(snapshot(fiveSessions(), profiles)), profiles)
}

func detailStore() Options {
	return testOptions(newFakeStore(snapshot([]*store.SessionView{sessionWithAgents()}, profiles)), profiles)
}

func singleStore() Options {
	single := profiles[:1]
	var sessions []*store.SessionView
	for _, s := range fiveSessions() {
		if s.ProfileLabel == "claude" {
			sessions = append(sessions, s)
		}
	}
	return testOptions(newFakeStore(snapshot(sessions, single)), single)
}

func manyStore() Options {
	var sessions []*store.SessionView
	for i := 0; i < 9; i++ {
		sessions = append(sessions, session("claude:m"+string(rune('0'+i)), func(s *store.SessionView) {
			s.DisplayTitle = "Many " + string(rune('0'+i))
		}))
	}
	return testOptions(newFakeStore(snapshot(sessions, profiles)), profiles)
}

func pendingStore() Options {
	sessions := []*store.SessionView{
		session("claude:p1", func(s *store.SessionView) {
			s.DisplayTitle, s.Kind = "Bg task", "background"
		}),
		session("claude:p2", func(s *store.SessionView) {
			s.DisplayTitle = "Idle task"
		}),
	}
	return testOptions(newFakeStore(snapshot(sessions, profiles)), profiles)
}

func withAppearance(o Options, c *style.AppearanceConfig) Options {
	o.Appearance = style.ResolveAppearance(c)
	return o
}

func asciiConfig() *style.AppearanceConfig {
	return &style.AppearanceConfig{Glyphs: &style.GlyphsConfig{Preset: "ascii"}}
}

func scenarios() []scenario {
	f := false
	absolute := "absolute"
	return []scenario{
		{name: "tree-100x30", opts: fiveStore, w: 100, h: 30},
		{name: "tree-80x24", opts: fiveStore, w: 80, h: 24},
		{name: "tree-40x14", opts: fiveStore, w: 40, h: 14},
		{name: "toosmall-39x14", opts: fiveStore, w: 39, h: 14},
		{name: "toosmall-40x13", opts: fiveStore, w: 40, h: 13},
		{name: "single-100x30", opts: singleStore, w: 100, h: 30},
		{name: "select-right-100x30", opts: fiveStore, w: 100, h: 30, keys: []string{"right", "right"}},
		{name: "detail-100x30", opts: detailStore, w: 100, h: 30, keys: []string{"right", "enter"}},
		{name: "detail-expand-100x30", opts: detailStore, w: 100, h: 30, keys: []string{"right", "enter", "enter"}},
		{name: "detail-tools-100x30", opts: detailStore, w: 100, h: 30, keys: []string{"right", "enter", "tab"}},
		{name: "detail-80x16", opts: detailStore, w: 80, h: 16, keys: []string{"right", "enter", "down", "down", "down", "down"}},
		{name: "help-100x30", opts: fiveStore, w: 100, h: 30, keys: []string{"?"}},
		{name: "help-40x14", opts: fiveStore, w: 40, h: 14, keys: []string{"?"}},
		{name: "profile-filter-100x30", opts: fiveStore, w: 100, h: 30, keys: []string{"p", "p"}},
		{name: "ascii-100x30", opts: func() Options { return withAppearance(detailStore(), asciiConfig()) }, w: 100, h: 30},
		{name: "ascii-detail-100x30", opts: func() Options { return withAppearance(detailStore(), asciiConfig()) },
			w: 100, h: 30, keys: []string{"right", "enter", "enter"}},
		{name: "absolute-notop-100x30", opts: func() Options {
			return withAppearance(fiveStore(), &style.AppearanceConfig{Layout: &style.LayoutConfig{Time: absolute, TopBar: &f}})
		}, w: 100, h: 30},
		{name: "empty-80x24", opts: func() Options {
			snap := snapshot(nil, profiles)
			snap.ScannedAt = 0
			return testOptions(newFakeStore(snap), profiles)
		}, w: 80, h: 24},
		{name: "scroll-80x24", opts: manyStore, w: 80, h: 24, keys: []string{"down", "down", "down"}},
		{name: "agents-tree-100x30", opts: detailStore, w: 100, h: 30},
		{name: "agents-tree-40x30", opts: detailStore, w: 40, h: 30},
		{name: "pending-100x30", opts: pendingStore, w: 100, h: 30},
		{name: "color-tree-100x30", opts: func() Options {
			o := fiveStore()
			o.Style = StyleFull
			return o
		}, w: 100, h: 30, keys: []string{"right"}, colors: true},
	}
}

func TestGolden(t *testing.T) {
	for _, sc := range scenarios() {
		t.Run(sc.name, func(t *testing.T) {
			a := newApp(sc.opts(), sc.w, sc.h)
			press(a, sc.keys...)
			got := screen(a)
			if sc.colors {
				got = a.Render()
			}
			checkGolden(t, sc.name, got)
		})
	}
}

func TestGoldenOnce(t *testing.T) {
	checkGolden(t, "loading-80", RenderOnce(Options{Profiles: profiles, Appearance: style.DefaultAppearance()}))
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden (run with -update): %v", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from the golden:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}
