package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/danielfnfaria/agtop/internal/closer"
	"github.com/danielfnfaria/agtop/internal/config"
	"github.com/danielfnfaria/agtop/internal/domain"
	"github.com/danielfnfaria/agtop/internal/store"
	"github.com/danielfnfaria/agtop/internal/ui/model"
	"github.com/danielfnfaria/agtop/internal/ui/style"
)

// Source is the part of the store the UI reads: the latest snapshot and a signal after each
// emission.
type Source interface {
	Snapshot() *store.Snapshot
	Updates() <-chan struct{}
}

// Closer closes a session after validating its record and process (see internal/closer).
type Closer interface {
	Close(card closer.Card) error
}

const (
	defaultNoticeMS = 4000
	clockInterval   = time.Second
	// treeIndicatorRows are the scroll indicators the body keeps, besides a session box, for the
	// top bar to show.
	treeIndicatorRows = 2
	// smallTerminalMessage replaces the whole screen below the minimum size.
	smallTerminalMessage = "terminal too small (min 40x14)"
)

type (
	snapshotMsg struct{ snap *store.Snapshot }
	clockMsg    struct{}
	spinMsg     struct{ seq int }
	noticeMsg   struct{ seq int }
	retainedMsg struct{ n int }
	signalMsg   struct{ code int }
	// closeResultMsg is the outcome of a close started by confirmClose: err is nil when SIGTERM
	// was sent. title and pid are the frozen identity the user confirmed.
	closeResultMsg struct {
		title string
		pid   int
		err   error
	}
)

// App is the Bubble Tea model of the viewer.
type App struct {
	profiles   []config.ProfileDir
	appearance style.Appearance
	render     renderer
	source     Source
	closer     Closer
	done       <-chan struct{}
	nowFn      func() int64
	loc        *time.Location
	noticeMS   int

	snap          *store.Snapshot
	width, height int
	nav           model.NavState
	tones         map[string]style.Color

	// treeTop and agentsTop keep the scroll of the tree and of the detail agents between frames.
	treeTop, agentsTop int
	clockNow, spinNow  int64
	spinning           bool
	spinSeq, noticeSeq int
	lastNotice         string
	retained           int
	exitCode           int
}

// newApp builds the model; width and height are the terminal size until the first resize.
func newApp(o Options, width, height int) *App {
	now := o.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	noticeMS := o.NoticeMS
	if noticeMS <= 0 {
		noticeMS = defaultNoticeMS
	}
	labels := make([]string, len(o.Profiles))
	for i, p := range o.Profiles {
		labels[i] = p.Label
	}
	a := &App{
		profiles:   o.Profiles,
		appearance: o.Appearance,
		render:     renderer{style: o.Style, links: o.Links},
		source:     o.Store,
		closer:     o.Closer,
		done:       o.done,
		nowFn:      now,
		loc:        o.Location,
		noticeMS:   noticeMS,
		width:      width,
		height:     height,
		nav:        model.InitialNav,
		tones:      style.ProfileToneMap(labels, o.Appearance.Theme.Profiles),
	}
	a.clockNow = now()
	a.spinNow = a.clockNow
	if o.Store != nil {
		a.snap = o.Store.Snapshot()
	}
	a.settle()
	return a
}

// Init starts the clock and the wait for store updates.
func (a *App) Init() tea.Cmd {
	return tea.Batch(a.clockTick(), a.waitSnapshot(), a.spinCmd())
}

func (a *App) clockTick() tea.Cmd {
	return tea.Tick(clockInterval, func(time.Time) tea.Msg { return clockMsg{} })
}

func (a *App) waitSnapshot() tea.Cmd {
	if a.source == nil {
		return nil
	}
	updates, src, done := a.source.Updates(), a.source, a.done
	return func() tea.Msg {
		select {
		case <-updates:
			return snapshotMsg{src.Snapshot()}
		case <-done:
			return nil
		}
	}
}

// Update handles a message and keeps the derived state (selection, scroll, timers) in sync.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
	case snapshotMsg:
		if msg.snap != nil {
			a.snap = msg.snap
		}
		cmds = append(cmds, a.waitSnapshot())
	case clockMsg:
		a.clockNow = a.nowFn()
		cmds = append(cmds, a.clockTick())
	case spinMsg:
		if msg.seq != a.spinSeq {
			return a, nil
		}
		a.spinNow = a.nowFn()
		a.spinning = false
	case noticeMsg:
		if msg.seq == a.noticeSeq {
			a.nav = model.Navigate(a.nav, model.Action{Type: model.ActionClearNotice}, a.tree().ctx)
		}
	case retainedMsg:
		a.retained = msg.n
	case signalMsg:
		a.exitCode = msg.code
		return a, tea.Quit
	case closeResultMsg:
		if msg.err != nil {
			a.nav.Notice = "cannot close \"" + msg.title + "\": " + msg.err.Error()
		} else {
			a.nav.Notice = "sent SIGTERM to \"" + msg.title + "\" (pid " + strconv.Itoa(msg.pid) + ")"
		}
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+z" {
			return a, tea.Suspend
		}
		if a.nav.Close.Key != "" {
			// A close confirmation is pending: only its own keys act.
			switch key {
			case "y", "enter":
				if cmd := a.confirmClose(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case "esc", "n":
				a.cancelClose()
			case "q", "ctrl+c":
				return a, tea.Quit
			default:
				return a, nil
			}
			cmds = append(cmds, a.settle()...)
			return a, tea.Batch(cmds...)
		}
		cmd, ok := model.KeyToCommand(key)
		if !ok {
			return a, nil
		}
		switch cmd.Kind {
		case model.CommandQuit:
			return a, tea.Quit
		case model.CommandRedraw:
			return a, tea.ClearScreen
		case model.CommandNav:
			a.nav = model.Navigate(a.nav, cmd.Nav, a.tree().ctx)
		case model.CommandClose:
			a.requestClose()
		}
	}
	cmds = append(cmds, a.settle()...)
	return a, tea.Batch(cmds...)
}

func (a *App) tree() treeModel {
	return buildTreeModel(a.snap, a.profiles, a.nav, a.width, a.appearance.Glyphs.Line)
}

// requestClose enters the close confirmation for the selected session, freezing its identity so a
// session changing under the cursor cannot slip through the confirmation.
func (a *App) requestClose() {
	if a.nav.Help || a.nav.View != model.ViewTree || a.nav.SelectedKey == "" {
		return
	}
	if a.width < model.MinTermColumns || a.height < model.MinTermRows {
		return
	}
	s := a.tree().find(a.nav.SelectedKey)
	if s == nil {
		return
	}
	a.nav.Close = model.ClosePrompt{
		Key:          s.Key,
		Profile:      s.ProfileLabel,
		PID:          s.PID,
		SessionID:    s.SessionID,
		StartedAt:    s.StartedAt,
		Provider:     string(s.Provider),
		ProcessStart: s.ProcessStart,
		Title:        s.DisplayTitle,
	}
	a.nav.Notice = ""
}

// cancelClose leaves the confirmation without closing.
func (a *App) cancelClose() {
	a.nav.Close = model.ClosePrompt{}
	a.nav.Notice = ""
}

// confirmClose validates the frozen session and returns the command that closes it, so the
// closer's I/O (reading the record, probing the process, signaling) never blocks Update; the
// outcome comes back as a closeResultMsg. The closer re-validates the record and the live process
// before signaling, so the frozen card here is a first line of defence, not the last. Failures
// that need no I/O are reported in the notice right away and return nil.
func (a *App) confirmClose() tea.Cmd {
	p := a.nav.Close
	a.nav.Close = model.ClosePrompt{}
	if p.Key == "" {
		return nil
	}
	if a.width < model.MinTermColumns || a.height < model.MinTermRows {
		a.nav.Notice = "cannot close \"" + p.Title + "\": terminal too small"
		return nil
	}
	if a.closer == nil {
		a.nav.Notice = "cannot close \"" + p.Title + "\": close is not available"
		return nil
	}
	card, ok := a.closeCard(p)
	if !ok {
		a.nav.Notice = "cannot close \"" + p.Title + "\": profile not found"
		return nil
	}
	c := a.closer
	return func() tea.Msg {
		return closeResultMsg{title: p.Title, pid: p.PID, err: c.Close(card)}
	}
}

// closeCard maps the frozen prompt to the card the closer reads, resolving the profile dir.
func (a *App) closeCard(p model.ClosePrompt) (closer.Card, bool) {
	for _, pr := range a.profiles {
		if pr.Label == p.Profile {
			return closer.Card{
				ProfileDir:   pr.Dir,
				PID:          p.PID,
				SessionID:    p.SessionID,
				StartedAt:    p.StartedAt,
				Provider:     domain.ProviderID(p.Provider),
				ProcessStart: p.ProcessStart,
			}, true
		}
	}
	return closer.Card{}, false
}

// confirmPrompt is the confirmation text of the pending close, from the frozen identity, or "".
func (a *App) confirmPrompt() string {
	if a.nav.Close.Key == "" {
		return ""
	}
	return fmt.Sprintf("close %q (pid %d)? y/Enter confirm, Esc/n cancel", a.nav.Close.Title, a.nav.Close.PID)
}

// recheckClose cancels a pending close confirmation when the session it froze no longer matches
// the cursor (title, pid, …) or the terminal shrank below the minimum. A confirmation must never
// act on a session the user did not see; this is re-run on every settle (after each snapshot).
func (a *App) recheckClose() {
	if a.nav.Close.Key == "" {
		return
	}
	if a.width < model.MinTermColumns || a.height < model.MinTermRows {
		a.nav.Close = model.ClosePrompt{}
		return
	}
	s := a.tree().find(a.nav.Close.Key)
	if s == nil ||
		s.ProfileLabel != a.nav.Close.Profile ||
		s.PID != a.nav.Close.PID ||
		s.SessionID != a.nav.Close.SessionID ||
		s.StartedAt != a.nav.Close.StartedAt ||
		s.ProcessStart != a.nav.Close.ProcessStart ||
		s.DisplayTitle != a.nav.Close.Title {
		a.nav.Close = model.ClosePrompt{}
		a.nav.Notice = "the session changed; close canceled"
	}
}

// settle fits the navigation to the snapshot, records the scroll of this frame and (re)arms the
// notice and spinner timers.
func (a *App) settle() []tea.Cmd {
	var cmds []tea.Cmd
	a.nav = model.SyncNav(a.nav, a.tree().ctx)
	a.recheckClose()
	f := a.compose()
	if f.treeShown {
		a.treeTop = f.viewport.Top
	}
	if f.detail != nil {
		a.agentsTop = f.detailGeo.start
	}
	if a.nav.Notice != a.lastNotice {
		a.lastNotice = a.nav.Notice
		a.noticeSeq++
		if a.nav.Notice != "" {
			seq := a.noticeSeq
			cmds = append(cmds, tea.Tick(time.Duration(a.noticeMS)*time.Millisecond,
				func(time.Time) tea.Msg { return noticeMsg{seq} }))
		}
	}
	if f.spinning && !a.spinning {
		a.spinning = true
		a.spinSeq++
		cmds = append(cmds, a.spinCmd())
	} else if !f.spinning && a.spinning {
		a.spinning = false
		a.spinSeq++
	}
	return cmds
}

// spinCmd ticks at the next spinner frame boundary, while a spinner is on screen.
func (a *App) spinCmd() tea.Cmd {
	if !a.spinning {
		return nil
	}
	seq := a.spinSeq
	wait := style.SpinnerFrameMS - a.nowFn()%style.SpinnerFrameMS
	return tea.Tick(time.Duration(wait)*time.Millisecond, func(time.Time) tea.Msg { return spinMsg{seq} })
}

// frame is the geometry of a screen, derived from the state (pure).
type frame struct {
	tooSmall   bool
	screenRows int
	tree       treeModel
	status     model.StatusBarModel
	top        model.TopBarModel
	showTop    bool
	bodyRows   int
	innerRows  int
	viewport   model.TreeViewport
	treeShown  bool
	detail     *store.SessionView
	detailGeo  detailGeometry
	spinning   bool
}

func (a *App) compose() frame {
	g, t := a.appearance.Glyphs, a.appearance.Theme
	var f frame
	f.screenRows = max(1, a.height-1)
	f.tooSmall = a.width < model.MinTermColumns || a.height < model.MinTermRows
	f.tree = a.tree()
	if a.nav.View == model.ViewDetail {
		f.detail = f.tree.find(a.nav.DetailKey)
	}
	hints := model.HintsFor(g)
	hint := hints.Tree
	if !f.tree.multiProfile {
		hint = model.SingleProfileTreeHints(g)
	}
	switch {
	case a.nav.Help:
		hint = hints.Help
	case f.detail != nil:
		hint = hints.Detail
	}
	f.status = model.NewStatusBarModel(model.StatusBarInput{
		Snapshot: a.snap, Profiles: a.profiles, ProfileFilter: f.tree.filter, Sessions: f.tree.sessions,
		Hint: hint, Columns: a.width, Retained: a.retained, Tones: a.tones, Glyphs: g, Theme: t,
	})
	f.top = a.topBar()
	belowTop := f.screenRows - model.StatusBarHeight(f.status)
	f.showTop = a.snap != nil && a.appearance.Layout.TopBar &&
		belowTop-model.TopBarHeight(f.top) >= f.tree.layout.SessionHeight+treeIndicatorRows
	topRows := 0
	if f.showTop {
		topRows = model.TopBarHeight(f.top)
	}
	f.bodyRows = belowTop - topRows
	notice := 0
	if a.nav.Notice != "" || a.nav.Close.Key != "" {
		notice = 1
	}
	f.innerRows = max(1, f.bodyRows-notice)
	f.viewport = f.tree.layout.Viewport(f.innerRows, a.nav.SelectedKey, a.treeTop)
	if f.tooSmall || a.snap == nil {
		f.detail = nil
		return f
	}
	f.treeShown = !a.nav.Help && f.detail == nil
	if f.detail != nil && !a.nav.Help {
		f.detailGeo = detailLayout(f.detail, a.nav, f.innerRows, a.agentsTop)
	}
	f.spinning = f.treeShown && len(f.tree.sessions) > 0 && treeSpinning(f.tree, f.viewport)
	return f
}

func (a *App) topBar() model.TopBarModel {
	in := model.TopBarInput{
		Profiles: a.profiles, Tones: a.tones, Now: a.clockNow, Columns: a.width, Loc: a.loc,
		Glyphs: a.appearance.Glyphs, Theme: a.appearance.Theme, NoSparkline: !a.appearance.Layout.Sparkline,
		Usage: map[string]domain.UsageView{}, ActivityByProfile: map[string]domain.ActivityHistogram{},
		Providers: map[string]domain.ProviderID{},
	}
	if a.snap != nil {
		for _, p := range a.snap.Profiles {
			if p.Usage != nil {
				in.Usage[p.Label] = *p.Usage
			}
			in.Providers[p.Label] = p.Provider
		}
		for _, h := range a.snap.ActivityByProfile {
			in.ActivityByProfile[h.Label] = h.Histogram
		}
	}
	return model.NewTopBarModel(in)
}

func (a *App) painter() painter {
	return painter{
		g: a.appearance.Glyphs, t: a.appearance.Theme, layout: a.appearance.Layout,
		now: a.clockNow, spin: a.spinNow, loc: a.loc, links: a.render.links,
	}
}

// View draws the screen: rows - 1 lines (the last terminal row stays blank).
func (a *App) View() tea.View {
	v := tea.NewView(a.Render())
	v.AltScreen = true
	return v
}

// Render is the text of the current screen.
func (a *App) Render() string {
	f := a.compose()
	p := a.painter()
	g, t := p.g, p.t
	c := newCanvas(a.width, f.screenRows)
	if f.tooSmall {
		c.line(0, 0, a.width, g.Ellipsis, fg(smallTerminalMessage, t.Warn))
		return a.render.render(c)
	}
	if a.snap == nil {
		return p.loadingScreen(a.render, a.width, a.profiles, a.source != nil)
	}
	y := 0
	if f.showTop {
		for _, line := range f.top.Lines {
			p.segments(c, 0, y, a.width, line)
			y++
		}
		p.divider(c, y)
		y++
	}
	bodyEnd := y + f.bodyRows
	c.clip(y, bodyEnd)
	if prompt := a.confirmPrompt(); prompt != "" {
		c.line(0, y, a.width, g.Ellipsis, fg(prompt, t.Warn))
		y++
	} else if a.nav.Notice != "" {
		c.line(0, y, a.width, g.Ellipsis, fg(a.nav.Notice, t.Warn))
		y++
	}
	switch {
	case a.nav.Help:
		p.drawHelp(c, y, f.innerRows, a.snap.Errors, a.profiles, a.tones)
	case f.detail != nil:
		var profile *config.ProfileDir
		for i := range a.profiles {
			if a.profiles[i].Label == f.detail.ProfileLabel {
				profile = &a.profiles[i]
			}
		}
		p.drawDetail(c, y, detailInput{
			session: f.detail, profile: profile, showProfile: f.tree.multiProfile,
			tone: a.tones[f.detail.ProfileLabel], nav: a.nav, geo: f.detailGeo, rows: f.innerRows,
		})
	case len(f.tree.sessions) == 0:
		msg := a.emptyMessage(f.tree.filter)
		w := min(ansi.StringWidth(msg), a.width)
		c.line((a.width-w)/2, y+(f.innerRows-1)/2, a.width, g.Ellipsis, fg(msg, t.Muted))
	default:
		p.drawTree(c, y, f.innerRows, f.tree, f.viewport, a.nav.SelectedKey, a.tones)
	}
	c.unclip()
	y = bodyEnd
	p.divider(c, y)
	y++
	if f.status.Rows == 2 {
		p.segments(c, 0, y, a.width, f.status.Left)
		hw := model.SegmentsWidth(f.status.Hint)
		p.segments(c, max(0, a.width-hw), y+1, a.width, f.status.Hint)
	} else {
		hw := model.SegmentsWidth(f.status.Hint)
		p.segments(c, 0, y, max(0, a.width-hw-model.MinSpacing), f.status.Left)
		p.segments(c, max(0, a.width-hw), y, hw, f.status.Hint)
	}
	return a.render.render(c)
}

func (a *App) emptyMessage(filter string) string {
	if filter != "" {
		return "no sessions in profile " + filter + " (p changes the filter)"
	}
	dirs := ""
	for i, p := range a.profiles {
		if i > 0 {
			dirs += ", "
		}
		dirs += p.Dir
	}
	if a.snap.ScannedAt == 0 {
		return "looking for sessions in " + dirs
	}
	for _, st := range a.snap.Profiles {
		if st.Unsupported {
			return domain.ProviderInfo(st.Provider).DisplayName + " support is not available yet (" + dirs + ")"
		}
	}
	name := "Claude Code"
	for i, p := range a.profiles {
		if id := config.ProviderOf(p); i == 0 {
			name = domain.ProviderInfo(id).DisplayName
		} else if domain.ProviderInfo(id).DisplayName != name {
			name = "agent"
			break
		}
	}
	return "no open " + name + " sessions in " + dirs
}

// segments draws model segments cut to width.
func (p painter) segments(c *canvas, x, y, width int, segs []model.Segment) {
	out := make([]seg, len(segs))
	for i, s := range segs {
		out[i] = seg{s.Text, sty{fg: s.Color, bold: s.Bold, inverse: s.Inverse, dim: s.Dim}}
	}
	c.line(x, y, width, p.g.Ellipsis, out...)
}

func (p painter) divider(c *canvas, y int) {
	line := ""
	for i := 0; i < c.w; i++ {
		line += p.g.Line.Horizontal
	}
	c.line(0, y, c.w, p.g.Ellipsis, fg(line, p.t.Muted))
}

// loadingScreen is the screen before the first snapshot: the watched directories, wrapped to
// width. loading is false without a store (no terminal: the sessions are never loaded).
func (p painter) loadingScreen(r renderer, width int, profiles []config.ProfileDir, loading bool) string {
	t := p.t
	line := func(segs ...seg) string {
		out := ""
		for _, s := range segs {
			out += r.styled(s.text, s.st)
		}
		return ansi.Wrap(out, max(1, width), "")
	}
	lines := []string{line(seg{"agtop", sty{bold: true}}), line(plain("Watched directories:"))}
	for _, pr := range profiles {
		lines = append(lines, line(plain(" - "), fg(pr.Label, t.Accent), plain(" "+pr.Dir+" "),
			fg("("+string(pr.Source)+")", t.Muted)))
	}
	status := "sessions not loaded (no terminal)"
	if loading {
		status = "loading sessions"
	}
	lines = append(lines, line(fg(status, t.Muted)), line(fg("q or Ctrl-C to quit", t.Muted)))
	return strings.Join(lines, "\n")
}
