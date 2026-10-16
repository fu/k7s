package tui

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fu/k7s/internal/pods"
)

const (
	flashFor    = 3 * time.Second // how long a changed cell stays highlighted
	keepDeleted = 8 * time.Second // how long a deleted pod lingers (struck through)
	maxEvents   = 1000
)

// Pod source messages carry the generation of the source that sent them, so
// late messages from a namespace we already switched away from are ignored.
type (
	podMsg struct {
		gen int
		ev  pods.Event
	}
	metricsMsg struct {
		gen int
		m   pods.Metrics
	}
	syncedMsg struct {
		gen int
	}
	watchErrMsg struct {
		gen int
		err error
	}
	kubeEventMsg struct {
		gen int
		e   pods.KubeEvent
	}
)

type column int

const (
	colNum column = iota
	colName
	colReady
	colStatus
	colRestarts
	colCPU
	colCPUR
	colCPUL
	colMem
	colMemR
	colMemL
	colAge
	numCols
)

var colTitles = [numCols]string{"#", "NAME", "READY", "STATUS", "RESTARTS", "CPU", "%CPU/R", "%CPU/L", "MEM", "%MEM/R", "%MEM/L", "AGE"}

var rightAligned = [numCols]bool{colNum: true, colCPU: true, colCPUR: true, colCPUL: true, colMem: true, colMemR: true, colMemL: true}

// When the terminal is too narrow: shorten pod names first, and only then
// give up the utilisation percentages.
var dropWhenNarrow = []column{colCPUR, colMemR, colCPUL, colMemL}

type rowState struct {
	row       pods.Row
	usage     pods.Usage
	hasUsage  bool
	cpuTrend  int // -1, 0, +1 versus the previous metrics poll
	memTrend  int
	changed   [numCols]time.Time
	addedAt   time.Time
	deletedAt time.Time
}

// eventLine is one change-log entry: a pod change (row set), or a line about
// another object (name set, e.g. "secret/web-tls", "deployment/web").
type eventLine struct {
	at     time.Time
	kind   pods.EventType
	row    pods.Row
	name   string
	detail string
}

type podsState struct {
	gen    int
	cancel context.CancelFunc

	rows           map[string]*rowState
	usage          map[string]pods.Usage
	containerUsage map[string]map[string]pods.Usage
	order          []string // sorted by name

	// The change log of the namespace: pods, secrets and Kubernetes events.
	events []eventLine
	// Events from before the pod list arrived wait for it, so the filter
	// can tell which ones are about listed objects.
	pending []pods.KubeEvent

	synced     bool
	metricsAt  time.Time
	metricsErr error
	watchErr   error
	watchErrAt time.Time
}

// startPods (re)starts the pod watch (and the namespace's event watch).
func (m *model) startPods() {
	p := &m.pods
	if p.cancel != nil {
		p.cancel()
	}
	gen := p.gen + 1
	ctx, cancel := context.WithCancel(m.ctx)
	*p = podsState{
		gen:            gen,
		cancel:         cancel,
		rows:           map[string]*rowState{},
		usage:          map[string]pods.Usage{},
		containerUsage: map[string]map[string]pods.Usage{},
	}
	send := m.send
	src := pods.NewSource(m.client, m.info.Namespace, m.info.Selector, m.info.Refresh)
	err := src.Start(ctx, pods.Handlers{
		Pod:     func(e pods.Event) { send(podMsg{gen, e}) },
		Metrics: func(mm pods.Metrics) { send(metricsMsg{gen, mm}) },
		Synced:  func() { send(syncedMsg{gen}) },
		Error:   func(err error) { send(watchErrMsg{gen, err}) },
		Event:   func(e pods.KubeEvent) { send(kubeEventMsg{gen, e}) },
	})
	if err != nil {
		p.watchErr, p.watchErrAt = err, time.Now()
	}
}

func (m *model) podsMsg(msg tea.Msg) {
	p := &m.pods
	switch msg := msg.(type) {
	case podMsg:
		if msg.gen == p.gen {
			p.apply(msg.ev)
		}
	case metricsMsg:
		if msg.gen == p.gen {
			p.applyMetrics(msg.m)
		}
	case syncedMsg:
		if msg.gen == p.gen && !p.synced {
			p.synced = true
			p.logInitialProblems()
			for _, e := range p.pending {
				m.applyKubeEvent(e)
			}
			p.pending = nil
		}
	case watchErrMsg:
		if msg.gen == p.gen {
			p.watchErr, p.watchErrAt = msg.err, time.Now()
		}
	case kubeEventMsg:
		if msg.gen == p.gen {
			if !p.synced {
				p.pending = append(p.pending, msg.e)
			} else {
				m.applyKubeEvent(msg.e)
			}
		}
	}
}

// stateKind marks change-log lines describing something that was already
// unhealthy when the watch started (there was no change to log for it).
const stateKind pods.EventType = "STATE"

func (p *podsState) logInitialProblems() {
	now := time.Now()
	for _, k := range p.order {
		r := p.rows[k].row
		if r.Health() == pods.Healthy || r.Health() == pods.Finished {
			continue
		}
		var details []string
		if msg := r.WaitMessage(); msg != "" {
			details = append(details, msg)
		}
		if r.Restarts > 0 {
			details = append(details, "restarts "+pods.Restarts(r))
		}
		if !r.LastExit.IsZero() {
			who := ""
			if len(r.Containers) > 1 {
				who = r.LastExitContainer + " "
			}
			details = append(details, "last exit "+who+r.LastExit.Describe())
		}
		p.logEvent(eventLine{at: now, kind: stateKind, row: r, detail: strings.Join(details, ", ")})
	}
}

// applyKubeEvent adds a Kubernetes event to the change log. With a filter,
// only events about listed objects count: listed pods and secrets, and the
// workloads (and ReplicaSets) of listed pods.
func (m *model) applyKubeEvent(e pods.KubeEvent) {
	if e.Reason == "BackOff" {
		return // already visible as restarts / CrashLoopBackOff / ImagePullBackOff
	}
	if m.info.Selector != "" && !m.eventAboutListed(e) {
		return
	}
	kind := pods.Normal
	if e.Type == "Warning" {
		kind = pods.Warn
	}
	line := eventLine{at: e.At, kind: kind, name: strings.ToLower(e.Kind) + "/" + e.Name}
	if e.Kind == "Pod" {
		line.name = e.Name
	}
	line.detail = e.Reason + ": " + e.Message
	if e.Count > 1 {
		line.detail += fmt.Sprintf(" (×%d)", e.Count)
	}
	m.pods.logEvent(line)
}

func (m *model) eventAboutListed(e pods.KubeEvent) bool {
	key := e.Namespace + "/" + e.Name
	switch e.Kind {
	case "Pod":
		_, ok := m.pods.rows[key]
		return ok
	case "Secret":
		_, ok := m.secrets.rows[key]
		return ok
	}
	for _, rs := range m.pods.rows {
		r := rs.row
		if e.Kind == r.WorkloadKind && e.Name == r.WorkloadName {
			return true
		}
		if e.Kind == "ReplicaSet" && r.WorkloadKind == "Deployment" && strings.HasPrefix(e.Name, r.WorkloadName+"-") {
			return true
		}
	}
	return false
}

const jumpWindow = 1500 * time.Millisecond

// typeNumber adds a typed digit to buf and returns the number typed so far;
// digits typed within jumpWindow of each other form one number.
func typeNumber(buf *string, at *time.Time, digit string) int {
	if time.Since(*at) > jumpWindow {
		*buf = ""
	}
	if *buf == "" && digit == "0" {
		return 0
	}
	*buf += digit
	*at = time.Now()
	var n int
	fmt.Sscan(*buf, &n)
	return n
}

func isDigit(key string) bool { return len(key) == 1 && key[0] >= '0' && key[0] <= '9' }

// jumpOr shows the number being typed, otherwise the screen's commands.
func jumpOr(buf string, at time.Time, commands string) string {
	if buf != "" && time.Since(at) < jumpWindow {
		return "go to #[" + buf + "]"
	}
	return commands
}

// ---- state updates -------------------------------------------------------

func (p *podsState) apply(e pods.Event) {
	now := time.Now()
	key := e.Row.Key()
	rs, exists := p.rows[key]

	switch {
	case e.Type == pods.Deleted:
		if exists {
			rs.row, rs.deletedAt = e.Row, now
		}
		p.logEvent(eventLine{at: now, kind: e.Type, row: e.Row})

	case !exists || !rs.deletedAt.IsZero():
		// New pod, or a pod re-created under the same name (StatefulSets).
		rs = &rowState{row: e.Row}
		if u, ok := p.usage[key]; ok {
			rs.usage, rs.hasUsage = u, true
		}
		if !e.Initial {
			rs.addedAt = now
			p.logEvent(eventLine{at: now, kind: pods.Added, row: e.Row})
		}
		p.rows[key] = rs

	default:
		var details []string
		mark := func(c column, name, from, to string) {
			if from != to {
				rs.changed[c] = now
				details = append(details, fmt.Sprintf("%s %s → %s", name, orNone(from), orNone(to)))
			}
		}
		old := rs.row
		mark(colStatus, "status", old.Status, e.Row.Status)
		if msg := e.Row.WaitMessage(); msg != "" && old.Status != e.Row.Status {
			details = append(details, msg)
		}
		mark(colReady, "ready", old.ReadyString(), e.Row.ReadyString())
		mark(colRestarts, "restarts", fmt.Sprint(old.Restarts), fmt.Sprint(e.Row.Restarts))
		if old.Node != e.Row.Node && e.Row.Node != "" {
			details = append(details, "scheduled on "+e.Row.Node)
		}
		// Explain a new container exit: why it stopped.
		if le := e.Row.LastExit; !le.IsZero() && !le.At.Equal(old.LastExit.At) {
			who := ""
			if len(e.Row.Containers) > 1 {
				who = e.Row.LastExitContainer + " "
			}
			details = append(details, "exit "+who+le.Describe())
		}
		rs.row = e.Row
		// Most MODIFIED events (resourceVersion, conditions, annotations)
		// change nothing visible; only log the ones that do.
		if len(details) > 0 {
			p.logEvent(eventLine{at: now, kind: pods.Modified, row: e.Row, detail: strings.Join(details, ", ")})
		}
	}
	p.resort()
}

func (p *podsState) applyMetrics(mm pods.Metrics) {
	if mm.Err != nil {
		p.metricsErr = mm.Err
		return
	}
	p.metricsErr, p.metricsAt = nil, mm.At
	p.usage, p.containerUsage = mm.Usage, mm.Containers
	for key, rs := range p.rows {
		u, ok := mm.Usage[key]
		if !ok {
			continue
		}
		if rs.hasUsage {
			rs.cpuTrend = trend(pods.CPU(rs.usage.CPU), pods.CPU(u.CPU), rs.usage.CPU, u.CPU)
			rs.memTrend = trend(pods.Mem(rs.usage.Mem), pods.Mem(u.Mem), rs.usage.Mem, u.Mem)
		}
		rs.usage, rs.hasUsage = u, true
	}
}

// trend compares displayed values so sub-Mi memory jitter doesn't flicker.
func trend(oldS, newS string, oldV, newV int64) int {
	switch {
	case oldS == newS:
		return 0
	case newV > oldV:
		return 1
	default:
		return -1
	}
}

// logEvent adds to the change log, kept in time order (events from before
// the watch started carry their own, older timestamps).
func (p *podsState) logEvent(e eventLine) {
	p.events = append(p.events, e)
	if n := len(p.events); n > 1 && p.events[n-2].at.After(e.at) {
		sort.SliceStable(p.events, func(i, j int) bool { return p.events[i].at.Before(p.events[j].at) })
	}
	if len(p.events) > maxEvents {
		p.events = p.events[len(p.events)-maxEvents:]
	}
}

func (p *podsState) purgeDeleted() {
	removed := false
	for k, rs := range p.rows {
		if !rs.deletedAt.IsZero() && time.Since(rs.deletedAt) > keepDeleted {
			delete(p.rows, k)
			removed = true
		}
	}
	if removed {
		p.resort()
	}
}

func (p *podsState) resort() {
	p.order = p.order[:0]
	for k := range p.rows {
		p.order = append(p.order, k)
	}
	sort.Strings(p.order)
}

// ---- rendering -----------------------------------------------------------

// podLines renders the pod table: its header and one line per pod (in
// order), the one with key selected highlighted.
func (m *model) podLines(selected string) (string, []string) {
	p := &m.pods
	now := time.Now()
	cells := make([][numCols]cell, len(p.order))
	for i, k := range p.order {
		cells[i] = m.rowCells(p.rows[k], i+1, now)
	}
	cols, widths := m.fitColumns(cells)
	colWidths := make([]int, len(cols))
	right := make([]bool, len(cols))
	for i, c := range cols {
		colWidths[i], right[i] = widths[c], rightAligned[c]
	}
	pick := func(all [numCols]cell) []cell {
		out := make([]cell, len(cols))
		for i, c := range cols {
			out[i] = all[c]
		}
		return out
	}
	var hdr [numCols]cell
	for c := range hdr {
		hdr[c] = cell{text: colTitles[c], style: stHeader}
	}
	lines := make([]string, len(cells))
	for i := range cells {
		lines[i] = renderCells(pick(cells[i]), colWidths, right, p.order[i] == selected, m.width)
	}
	return renderCells(pick(hdr), colWidths, right, false, m.width), lines
}

func (m *model) emptyMessage() string {
	if !m.pods.synced {
		return "  waiting for pod list…"
	}
	return "  no pods"
}

func (m *model) rowCells(rs *rowState, num int, now time.Time) [numCols]cell {
	r := rs.row
	var c [numCols]cell
	c[colNum] = cell{text: fmt.Sprint(num), style: stDim}
	c[colName] = cell{text: r.Name, middle: true}
	for _, local := range m.forwardsOf(r.Key()) {
		c[colName].text += fmt.Sprintf(" ⇄:%d", local)
	}
	c[colReady] = cell{text: r.ReadyString()}
	// Just the light: the change log says what the state is and why.
	c[colStatus] = cell{text: "●", style: statusStyle(r)}
	c[colRestarts] = cell{text: pods.Restarts(r)}
	if r.Restarts > 0 {
		c[colRestarts].style = stYellow
	}
	c[colAge] = cell{text: pods.Age(r.Created)}

	if rs.hasUsage {
		c[colCPU] = cell{text: pods.CPU(rs.usage.CPU) + arrow(rs.cpuTrend), style: trendStyle(rs.cpuTrend)}
		c[colMem] = cell{text: pods.Mem(rs.usage.Mem) + arrow(rs.memTrend), style: trendStyle(rs.memTrend)}
		c[colCPUR] = pctCell(rs.usage.CPU, r.CPUReq, false)
		c[colCPUL] = pctCell(rs.usage.CPU, r.CPULim, true)
		c[colMemR] = pctCell(rs.usage.Mem, r.MemReq, false)
		c[colMemL] = pctCell(rs.usage.Mem, r.MemLim, true)
	} else {
		for _, k := range []column{colCPU, colCPUR, colCPUL, colMem, colMemR, colMemL} {
			c[k] = cell{text: "-", style: stDim}
		}
	}

	switch {
	case !rs.deletedAt.IsZero():
		for k := range c {
			c[k].style = stDeleted
		}
	case !rs.addedAt.IsZero() && now.Sub(rs.addedAt) < flashFor:
		c[colName].style = stGreen.Bold(true)
		fallthrough
	default:
		for k := range c {
			if !rs.changed[k].IsZero() && now.Sub(rs.changed[k]) < flashFor {
				c[k].style = stFlash
			}
		}
	}
	return c
}

func (m *model) fitColumns(rows [][numCols]cell) ([]column, [numCols]int) {
	var widths [numCols]int
	for c := column(0); c < numCols; c++ {
		widths[c] = lipgloss.Width(colTitles[c])
		for _, r := range rows {
			widths[c] = max(widths[c], lipgloss.Width(r[c].text))
		}
	}
	var shown [numCols]bool
	for c := range shown {
		shown[c] = true
	}
	total := func() int {
		t := 0
		for c := column(0); c < numCols; c++ {
			if shown[c] {
				t += widths[c] + 2
			}
		}
		return t - 2
	}
	drop := func(cs []column) {
		for _, c := range cs {
			if total() <= m.width {
				return
			}
			shown[c] = false
		}
	}
	shrinkName := func(floor int) {
		if over := total() - m.width; over > 0 {
			widths[colName] = max(min(floor, widths[colName]), widths[colName]-over)
		}
	}
	shrinkName(32)
	drop(dropWhenNarrow)
	shrinkName(12)

	var cols []column
	for c := column(0); c < numCols; c++ {
		if shown[c] {
			cols = append(cols, c)
		}
	}
	return cols, widths
}

// podCounts counts live pods by health, for the status line.
func (m *model) podCounts() (healthy, progressing, failing int) {
	for _, rs := range m.pods.rows {
		if !rs.deletedAt.IsZero() {
			continue
		}
		switch rs.row.Health() {
		case pods.Healthy:
			healthy++
		case pods.Progressing:
			progressing++
		case pods.Failing:
			failing++
		}
	}
	return
}

// changesPane renders a scrollable change log of total entries in n lines,
// newest at the bottom. back is how many lines it is scrolled up; entries
// can span several lines (wrapping, one line per problem resource).
func (m *model) changesPane(n, total int, back *int, line func(i int) string, empty string) string {
	// Wrap entries from the newest backwards, only as many as are needed.
	var all []string
	for i := total - 1; i >= 0 && len(all) < *back+n; i-- {
		all = append(wrapChange(line(i), m.width), all...)
	}
	*back = max(0, min(*back, len(all)-n))

	var b strings.Builder
	title := fmt.Sprintf("─ changes (%d) ", total)
	if *back > 0 {
		title += fmt.Sprintf("▲ scrolled up %d lines (a to go down) ", *back)
	}
	b.WriteString(stDim.Render(title + strings.Repeat("─", max(0, m.width-lipgloss.Width(title)))))
	b.WriteByte('\n')

	end := len(all) - *back
	lines := all[max(0, end-n):end]
	if len(lines) == 0 {
		lines = append(lines, stDim.Render("  "+empty))
	}
	for i := 0; i < n; i++ {
		if i < len(lines) {
			b.WriteString(lines[i])
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// changeIndent lines wrapped continuation lines up after the timestamp.
const changeIndent = 9

// wrapChange soft-wraps one change entry. Its first line starts at the left
// edge; wrapped parts and extra lines ("\n") are indented under the text.
func wrapChange(s string, width int) []string {
	var out []string
	for i, part := range strings.Split(s, "\n") {
		first := changeIndent
		if i == 0 {
			first = 0
		}
		out = append(out, wrap(part, width, first, changeIndent)...)
	}
	return out
}

func (m *model) eventLineView(e eventLine) string {
	kind := fmt.Sprintf("%-8s", e.kind)
	switch e.kind {
	case pods.Added:
		kind = stGreen.Render(kind)
	case pods.Deleted:
		kind = stRed.Render(kind)
	case pods.Warn:
		kind = stRed.Bold(true).Render(kind)
	case pods.Normal:
		kind = stDim.Render(kind)
	case stateKind, forwardKind:
		kind = stCyan.Render(kind)
	default:
		kind = stYellow.Render(kind)
	}
	ts := stDim.Render(e.at.Format("15:04:05"))
	switch {
	case e.kind == pods.Warn:
		return fmt.Sprintf("%s %s %s  %s", ts, kind, e.name, stYellow.Render(e.detail))
	case e.name != "":
		s := fmt.Sprintf("%s %s %s", ts, kind, e.name)
		if e.detail != "" {
			s += stDim.Render("  " + e.detail)
		}
		return s
	}
	s := fmt.Sprintf("%s %s %s  %s %s", ts, kind, e.row.Name,
		statusStyle(e.row).Render(e.row.Status), e.row.ReadyString())
	if e.detail != "" {
		s += stDim.Render("  " + e.detail)
	}
	return s
}

// ---- helpers -------------------------------------------------------------

// pulse is a dot that breathes once per metrics refresh: brightest the moment
// fresh metrics arrive, darkest halfway to the next refresh.
func (m *model) pulse() string {
	period := m.info.Refresh.Seconds()
	since := time.Since(m.pods.metricsAt).Seconds()
	if m.pods.metricsAt.IsZero() {
		since = float64(time.Now().UnixMilli()) / 1000
	}
	phase := math.Mod(since, period) / period
	k := (1 + math.Cos(2*math.Pi*phase)) / 2 // 1 at a refresh → 0 → 1
	mix := func(from, to float64) int { return int(from + (to-from)*k) }
	color := fmt.Sprintf("#%02x%02x%02x", mix(0x0b, 0x00), mix(0x4f, 0xe5), mix(0x5c, 0xff))
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render("●")
}

// statusStyle is the traffic light for a pod: green running, amber on its
// way (pending, creating, initializing), red terminating or failing.
func statusStyle(r pods.Row) lipgloss.Style {
	if r.Status == "Terminating" {
		return stRed
	}
	switch r.Health() {
	case pods.Healthy:
		return stGreen
	case pods.Progressing:
		return stAmber
	case pods.Finished:
		return stDim
	}
	return stRed.Bold(true)
}

func pctCell(v, of int64, isLimit bool) cell {
	p := pods.PctValue(v, of)
	c := cell{text: pods.Pct(v, of)}
	switch {
	case p < 0:
		c.style = stDim
	case isLimit && p >= 90, !isLimit && p >= 150:
		c.style = stRed.Bold(true)
	case isLimit && p >= 70, !isLimit && p >= 100:
		c.style = stYellow
	}
	return c
}

func arrow(t int) string {
	switch t {
	case 1:
		return "↑"
	case -1:
		return "↓"
	}
	return " "
}

func trendStyle(t int) lipgloss.Style {
	switch t {
	case 1:
		return stRed
	case -1:
		return stGreen
	}
	return lipgloss.NewStyle()
}
