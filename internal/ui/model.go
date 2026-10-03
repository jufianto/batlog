package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// View is one of the main area's views.
type View int

const (
	BatteryView View = iota
	AppsView
	ReportView
	HealthView
	numViews
)

var viewNames = [numViews]string{"Battery", "Apps", "Report", "Health"}

const (
	sideW = 30 // the sidebar, borders included
	minW  = 80
	minH  = 24
	// refreshEvery matches the recorder's tick.
	refreshEvery = time.Minute
	// fps: the screen changes on a key press or once a minute, so a tenth
	// of Bubble Tea's default 60 is plenty and costs less idle CPU.
	fps = 10
	// failLimit: a read that fails this many refreshes in a row is shown;
	// one failure (the recorder writing) is retried quietly.
	failLimit = 3
)

// Options configure a Model.
type Options struct {
	Color bool
	Now   func() time.Time // time.Now when nil
	// static leaves out the refresh timer: Snapshot runs every command
	// to completion, and a timer never completes.
	static bool
}

// Model is the whole UI's state. Rendering is a pure function of it.
type Model struct {
	src    Source
	ctx    context.Context
	now    func() time.Time
	st     styles
	static bool

	w, h int
	view View
	rng  Range
	help bool
	note string // an error or warning for the key line, until the next key

	live    *Live
	hist    *History
	histFor rangeKey
	apps    *Apps
	appsFor rangeKey
	report  []string
	repFor  rangeKey
	repOK   bool
	health  *HealthDays

	bat      list
	batOpen  bool
	sessText []string
	sessFor  string
	appl     list
	appOpen  bool
	series   []Point
	serFor   string
	scroll   [numViews]int // the report's and health view's first line
	fails    map[string]int
	loadedAt time.Time
}

// list is a selection kept by ID across refreshes.
type list struct {
	cur, top int
	id       string
}

// New makes the model; Run starts it.
func New(ctx context.Context, src Source, o Options) Model {
	if o.Now == nil {
		o.Now = time.Now
	}
	return Model{src: src, ctx: ctx, now: o.Now, st: newStyles(o.Color), static: o.static,
		rng: MakeRange(o.Now(), false, 0), fails: map[string]int{}}
}

// Run shows the UI until the user quits.
func Run(ctx context.Context, src Source, o Options, opts ...tea.ProgramOption) error {
	_, err := tea.NewProgram(New(ctx, src, o), append([]tea.ProgramOption{tea.WithContext(ctx), tea.WithFPS(fps)}, opts...)...).Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil // Ctrl-C through the context
	}
	return err
}

type (
	tickMsg time.Time
	liveMsg struct {
		v   Live
		err error
	}
	dataMsg struct {
		view   View
		key    rangeKey
		hist   *History
		apps   *Apps
		report *string
		health *HealthDays
		err    error
	}
	sessMsg struct {
		id   string
		text string
		err  error
	}
	seriesMsg struct {
		key string
		pts []Point
		err error
	}
)

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.loadLive(), m.loadView(), m.tick())
}

func (m Model) tick() tea.Cmd {
	if m.static {
		return nil
	}
	return tea.Tick(refreshEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) loadLive() tea.Cmd {
	src, ctx := m.src, m.ctx
	return func() tea.Msg {
		v, err := src.Live(ctx)
		return liveMsg{v, err}
	}
}

// loadView reads the current view's data for the current range.
func (m Model) loadView() tea.Cmd {
	src, ctx, r, v := m.src, m.ctx, m.rng, m.view
	return func() tea.Msg {
		msg := dataMsg{view: v, key: r.key()}
		switch v {
		case BatteryView:
			h, err := src.History(ctx, r)
			msg.hist, msg.err = &h, err
		case AppsView:
			a, err := src.Apps(ctx, r)
			msg.apps, msg.err = &a, err
		case ReportView:
			s, err := src.Report(ctx, r)
			msg.report, msg.err = &s, err
		case HealthView:
			h, err := src.Health(ctx)
			msg.health, msg.err = &h, err
		}
		return msg
	}
}

// stale reports whether the current view lacks data for the current range.
func (m Model) stale() bool {
	k := m.rng.key()
	switch m.view {
	case BatteryView:
		return m.hist == nil || m.histFor != k
	case AppsView:
		return m.apps == nil || m.appsFor != k
	case ReportView:
		return !m.repOK || m.repFor != k
	}
	return m.health == nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.clampLists()
		return m, nil
	case tickMsg:
		if m.rng.Current() {
			m.rng = MakeRange(m.now(), m.rng.Week, 0)
		}
		cmds := []tea.Cmd{m.loadLive(), m.loadView(), m.tick()}
		if m.batOpen {
			if s := m.selectedSession(); s != "" {
				cmds = append(cmds, m.loadSession(s))
			}
		}
		return m, tea.Batch(cmds...)
	case liveMsg:
		if m.failed("live", msg.err, m.live == nil) {
			return m, nil
		}
		m.live = &msg.v
		return m, nil
	case dataMsg:
		return m.gotData(msg), nil
	case sessMsg:
		if msg.id != m.selectedSession() || m.failed("session", msg.err, true) {
			return m, nil
		}
		m.sessText, m.sessFor = splitLines(msg.text), msg.id
		return m, nil
	case seriesMsg:
		if msg.key != m.seriesKey() || m.failed("series", msg.err, true) {
			return m, nil
		}
		m.series, m.serFor = msg.pts, msg.key
		return m, nil
	case tea.KeyPressMsg:
		m.note = ""
		return m.key(msg.String())
	}
	return m, nil
}

// failed counts a failed read and decides whether to show it: at once when
// there is nothing else to show, else after failLimit in a row. A Warning
// is a success with a note.
func (m *Model) failed(what string, err error, nothing bool) bool {
	var w Warning
	switch {
	case err == nil:
		m.fails[what] = 0
		return false
	case errors.As(err, &w):
		m.fails[what] = 0
		m.note = "warning: " + string(w)
		return false
	}
	m.fails[what]++
	if nothing || m.fails[what] >= failLimit {
		m.note = err.Error()
	}
	return true
}

func (m Model) gotData(msg dataMsg) Model {
	if msg.view != HealthView && msg.key != m.rng.key() {
		return m // loaded for a range no longer shown
	}
	switch msg.view {
	case BatteryView:
		if m.failed("battery", msg.err, m.hist == nil) {
			return m
		}
		m.hist, m.histFor = msg.hist, msg.key
		m.bat = keep(m.bat, ids(batItems(*m.hist)))
	case AppsView:
		if m.failed("apps", msg.err, m.apps == nil) {
			return m
		}
		m.apps, m.appsFor = msg.apps, msg.key
		var names []string
		for _, r := range m.apps.Rows {
			names = append(names, r.App)
		}
		m.appl = keep(m.appl, names)
	case ReportView:
		if m.failed("report", msg.err, !m.repOK) {
			return m
		}
		m.report, m.repFor, m.repOK = splitLines(*msg.report), msg.key, true
	case HealthView:
		if m.failed("health", msg.err, m.health == nil) {
			return m
		}
		m.health = msg.health
	}
	m.loadedAt = m.now()
	m.clampLists()
	return m
}

// keep moves the cursor back to the selected ID, or to the top when it is gone.
func keep(l list, ids []string) list {
	for i, id := range ids {
		if id == l.id {
			l.cur = i
			return l
		}
	}
	l.cur, l.top = 0, 0
	if len(ids) > 0 {
		l.id = ids[0]
	} else {
		l.id = ""
	}
	return l
}

func (m Model) key(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.help = !m.help
		return m, nil
	case "esc":
		switch {
		case m.help:
			m.help = false
		case m.view == BatteryView:
			m.batOpen = false
		case m.view == AppsView:
			m.appOpen = false
		}
		return m, nil
	case "1", "2", "3", "4":
		return m.switchTo(View(k[0] - '1'))
	case "tab":
		return m.switchTo((m.view + 1) % numViews)
	case "shift+tab":
		return m.switchTo((m.view + numViews - 1) % numViews)
	case "t":
		return m.setRange(false, 0)
	case "w":
		return m.setRange(true, 0)
	case "[":
		if m.live != nil && m.live.FirstTS > 0 && m.rng.From.Unix() > m.live.FirstTS {
			return m.setRange(m.rng.Week, m.rng.Back+1)
		}
		m.note = "no data before " + m.rng.Label()
		return m, nil
	case "]":
		if m.rng.Back > 0 {
			return m.setRange(m.rng.Week, m.rng.Back-1)
		}
		return m, nil
	case "r":
		return m, tea.Batch(m.loadLive(), m.loadView())
	case "enter":
		return m.open()
	}
	if m.help {
		return m, nil
	}
	return m.move(k), nil
}

func (m Model) switchTo(v View) (tea.Model, tea.Cmd) {
	m.view, m.help = v, false
	if m.stale() {
		return m, m.loadView()
	}
	return m, nil
}

func (m Model) setRange(week bool, back int) (tea.Model, tea.Cmd) {
	m.rng = MakeRange(m.now(), week, back)
	m.batOpen, m.appOpen, m.help = false, false, false
	m.scroll[ReportView] = 0
	return m, m.loadView()
}

func (m Model) open() (tea.Model, tea.Cmd) {
	switch {
	case m.view == BatteryView && m.hist != nil && len(batItems(*m.hist)) > 0:
		m.batOpen = true
		if id := m.selectedSession(); id != "" && id != m.sessFor {
			m.sessText = nil
			return m, m.loadSession(id)
		}
	case m.view == AppsView && m.apps != nil && len(m.apps.Rows) > 0:
		m.appOpen = true
		if k := m.seriesKey(); k != m.serFor {
			m.series = nil
			return m, m.loadSeries()
		}
	}
	return m, nil
}

func (m Model) loadSession(id string) tea.Cmd {
	src, ctx := m.src, m.ctx
	return func() tea.Msg {
		t, err := src.Session(ctx, id)
		return sessMsg{id, t, err}
	}
}

func (m Model) loadSeries() tea.Cmd {
	src, ctx, r, app, key := m.src, m.ctx, m.rng, m.appl.id, m.seriesKey()
	return func() tea.Msg {
		pts, err := src.AppSeries(ctx, app, r)
		return seriesMsg{key, pts, err}
	}
}

// selectedSession is the battery session under the cursor, "" for a charge.
func (m Model) selectedSession() string {
	if id, ok := strings.CutPrefix(m.bat.id, "s:"); ok {
		return id
	}
	return ""
}

func (m Model) seriesKey() string {
	return fmt.Sprint(m.appl.id, m.rng.key())
}

// move handles the cursor and scrolling keys.
func (m Model) move(k string) Model {
	step := map[string]int{"up": -1, "k": -1, "down": 1, "j": 1}[k]
	page := m.bodyH() - 2
	switch k {
	case "pgup":
		step = -page
	case "pgdown":
		step = page
	case "g", "home":
		step = -1 << 30
	case "G", "end":
		step = 1 << 30
	}
	if step == 0 {
		return m
	}
	switch {
	case m.view == BatteryView && !m.batOpen && m.hist != nil:
		items := ids(batItems(*m.hist))
		m.bat = moveList(m.bat, items, step, m.batListH())
	case m.view == AppsView && !m.appOpen && m.apps != nil:
		var names []string
		for _, r := range m.apps.Rows {
			names = append(names, r.App)
		}
		m.appl = moveList(m.appl, names, step, m.appsListH())
	case m.view == ReportView || m.view == HealthView:
		m.scroll[m.view] = max(0, m.scroll[m.view]+step)
		m.clampLists()
	}
	return m
}

func moveList(l list, ids []string, step, h int) list {
	if len(ids) == 0 {
		return l
	}
	l.cur = min(max(l.cur+step, 0), len(ids)-1)
	l.id = ids[l.cur]
	return scrollTo(l, h)
}

// scrollTo keeps the cursor inside a window h rows tall.
func scrollTo(l list, h int) list {
	h = max(h, 1)
	if l.cur < l.top {
		l.top = l.cur
	}
	if l.cur >= l.top+h {
		l.top = l.cur - h + 1
	}
	return l
}

// clampLists keeps scroll positions valid after a resize or new data.
func (m *Model) clampLists() {
	m.bat = scrollTo(m.bat, m.batListH())
	m.appl = scrollTo(m.appl, m.appsListH())
	if m.view == ReportView {
		m.scroll[ReportView] = min(m.scroll[ReportView], max(0, len(m.report)-m.bodyH()))
	}
	if m.view == HealthView {
		m.scroll[HealthView] = min(m.scroll[HealthView], max(0, len(m.healthLines(m.mainW()-2))-m.bodyH()))
	}
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// mainW and bodyH are the main box's outer width and inner height.
func (m Model) mainW() int { return m.w - sideW }
func (m Model) bodyH() int { return m.h - 1 - 2 }

func (m Model) View() tea.View {
	v := tea.NewView(m.Render())
	v.AltScreen = true
	v.WindowTitle = "batlog"
	return v
}

// Render is the screen as text.
func (m Model) Render() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	if m.w < minW || m.h < minH {
		return fmt.Sprintf("make the window at least %d×%d (now %d×%d)", minW, minH, m.w, m.h)
	}
	if m.help {
		return strings.Join(box("help", helpLines(), m.w, m.h-1, m.st), "\n") + "\n" + m.keyLine()
	}
	side := box("batlog", m.sidebar(sideW-2), sideW, m.h-1, m.st)
	title, body := m.main(m.mainW()-2, m.bodyH())
	main := box(title, body, m.mainW(), m.h-1, m.st)
	var b strings.Builder
	for i := range side {
		b.WriteString(side[i] + main[i] + "\n")
	}
	b.WriteString(m.keyLine())
	return b.String()
}

func (m Model) main(w, h int) (string, []string) {
	title := fmt.Sprintf("%d %s · %s", m.view+1, viewNames[m.view], m.rng.Label())
	if m.view == HealthView {
		title = "4 Health"
	}
	switch m.view {
	case BatteryView:
		return m.batteryView(title, w, h)
	case AppsView:
		return m.appsView(title, w, h)
	case ReportView:
		return title, m.reportView(w, h)
	}
	return title, m.healthView(w, h)
}

func (m Model) keyLine() string {
	if m.note != "" {
		return fit(" "+m.st.paint(m.st.warn, m.note), m.w)
	}
	keys := "1-4 view · ↑↓ select · enter open · t today · w week · [ ] earlier/later · r refresh · ? help · q quit"
	switch {
	case m.help:
		keys = "esc close · q quit"
	case m.batOpen && m.view == BatteryView, m.appOpen && m.view == AppsView:
		keys = "esc back · 1-4 view · t today · w week · [ ] earlier/later · r refresh · q quit"
	case m.view == ReportView || m.view == HealthView:
		keys = "1-4 view · ↑↓ scroll · t today · w week · [ ] earlier/later · r refresh · ? help · q quit"
	}
	return fit(" "+m.st.paint(m.st.sleep, keys), m.w)
}

func helpLines() []string {
	return []string{
		"Keys",
		"  1-4, Tab, Shift-Tab   switch views",
		"  ↑ ↓  k j  PgUp PgDn   move the selection or scroll",
		"  g G                   first, last",
		"  Enter / Esc           open a row's detail / back",
		"  t / w                 today / the last 7 days",
		"  [ / ]                 the day or week before / after",
		"  r                     refresh now (it also does every minute)",
		"  q, Ctrl-C             quit",
		"",
		"Views",
		"  1 Battery   battery % over the range, then battery sessions and",
		"              charges (⚡). Enter: a session's top apps, a charge's",
		"              time to full and time at 100%.",
		"  2 Apps      which apps used the most energy, and what each cost",
		"              the battery. Enter: an app's energy by hour or day.",
		"  3 Report    the daily or weekly report.",
		"  4 Health    battery health now, and per day since recording began.",
		"",
		"Chart: █ on battery · ▓ on AC (green) · ░ asleep · blank: no data",
		"batlog ui only reads; it never changes your Mac or its data.",
	}
}
