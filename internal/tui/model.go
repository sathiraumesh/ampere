// Package tui is the terminal UI of "ampere top": a live, top-like view of
// the machine's power and of every process and container. It only shows
// frames; where they come from (reading /proc, cgroups and RAPL) is up to
// the Sampler it is given.
package tui

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Tabs of the table.
const (
	tabProcesses = iota
	tabContainers
)

// historyLen is how many power values the sparkline shows.
const historyLen = 40

// Messages.
type (
	tickMsg  struct{}
	frameMsg struct {
		f   Frame
		ok  bool
		err error
	}
)

// Sampler produces the frames to show. Step takes a reading; it returns ok
// false (and no error) when it has no frame yet, e.g. on the first call.
type Sampler interface {
	Step() (f Frame, ok bool, err error)
}

// Run shows the UI until the user quits, taking a sample every interval.
func Run(s Sampler, interval time.Duration, in io.Reader, out io.Writer) error {
	p := tea.NewProgram(newModel(s, interval), tea.WithAltScreen(),
		tea.WithInput(in), tea.WithOutput(out))
	_, err := p.Run()
	return err
}

// model is the state of "ampere top".
type model struct {
	s        Sampler
	interval time.Duration

	frame   Frame
	have    bool  // a frame has arrived
	err     error // last sampling error, shown until the next good frame
	history []float64

	tab    int
	offset int // first table row shown (scrolling)
	width  int
	height int

	// search filters the rows (see matches). While typing is true, keys
	// edit search instead of controlling the view.
	search string
	typing bool
}

func newModel(s Sampler, interval time.Duration) model {
	return model{s: s, interval: interval, width: 100, height: 30}
}

func (m model) Init() tea.Cmd { return m.tick() }

// tick waits one interval, then asks for a sample. Samples are taken one
// after another (sample → frame → tick → sample), never at the same time.
func (m model) tick() tea.Cmd {
	return tea.Tick(m.interval, func(time.Time) tea.Msg { return tickMsg{} })
}

func sample(s Sampler) tea.Cmd {
	return func() tea.Msg {
		f, ok, err := s.Step()
		return frameMsg{f: f, ok: ok, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.offset = m.clampOffset(m.offset)
		return m, nil

	case tickMsg:
		return m, sample(m.s)

	case frameMsg:
		switch {
		case msg.err != nil:
			m.err = msg.err
		case msg.ok:
			m.frame, m.have, m.err = msg.f, true, nil
			m.history = append(m.history, msg.f.PowerW)
			if len(m.history) > historyLen {
				m.history = m.history[len(m.history)-historyLen:]
			}
			m.offset = m.clampOffset(m.offset)
		}
		return m, m.tick()

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.typing {
			return m.typeSearch(msg), nil
		}
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "esc":
			if m.search == "" {
				return m, tea.Quit
			}
			m.search, m.offset = "", 0 // first esc clears the search
		case "/":
			m.typing = true
		case "p":
			m.tab, m.offset = tabProcesses, 0
		case "c":
			m.tab, m.offset = tabContainers, 0
		case "tab":
			m.tab, m.offset = 1-m.tab, 0
		case "down", "j":
			m.offset = m.clampOffset(m.offset + 1)
		case "up", "k":
			m.offset = m.clampOffset(m.offset - 1)
		case "pgdown", " ":
			m.offset = m.clampOffset(m.offset + m.tableHeight())
		case "pgup":
			m.offset = m.clampOffset(m.offset - m.tableHeight())
		case "home", "g":
			m.offset = 0
		}
		return m, nil
	}
	return m, nil
}

// typeSearch handles a key while the search is being typed: text is added,
// backspace deletes, enter keeps the search, esc cancels it.
func (m model) typeSearch(k tea.KeyMsg) model {
	switch k.Type {
	case tea.KeyEnter:
		m.typing = false
	case tea.KeyEsc:
		m.typing, m.search = false, ""
	case tea.KeyBackspace:
		if r := []rune(m.search); len(r) > 0 {
			m.search = string(r[:len(r)-1])
		}
	case tea.KeyRunes, tea.KeySpace:
		m.search += string(k.Runes)
		if k.Type == tea.KeySpace {
			m.search += " "
		}
	}
	m.offset = 0
	return m
}

// allRows are the rows of the current tab, before the search.
func (m model) allRows() []Row {
	if m.tab == tabContainers {
		return m.frame.Containers
	}
	return m.frame.Processes
}

// rows are the rows of the current tab that match the search.
func (m model) rows() []Row {
	return filter(m.allRows(), m.search)
}

// filter keeps the rows that match search; all of them if it is empty.
func filter(rows []Row, search string) []Row {
	q := strings.ToLower(strings.TrimSpace(search))
	if q == "" {
		return rows
	}
	var out []Row
	for _, r := range rows {
		if matches(r, q) {
			out = append(out, r)
		}
	}
	return out
}

// matches reports whether the row matches q (lowercase), as part of:
// a process's command or PID; a container's name, ID or runtime.
func matches(r Row, q string) bool {
	if r.Container != "" {
		return strings.Contains(strings.ToLower(r.Name), q) ||
			strings.HasPrefix(r.Container, q) ||
			strings.Contains(r.Runtime, q)
	}
	return strings.Contains(strings.ToLower(r.Comm), q) ||
		strings.HasPrefix(strconv.Itoa(r.PID), q)
}

// headerLines is how many lines the view uses above the table rows.
const headerLines = 9

// tableHeight is how many table rows fit on the screen.
func (m model) tableHeight() int { return max(1, m.height-headerLines-2) }

func (m model) clampOffset(o int) int {
	return max(0, min(o, len(m.rows())-m.tableHeight()))
}

// Styles.
var (
	orange  = lipgloss.Color("#F79009")
	muted   = lipgloss.Color("#98A2B3")
	sTitle  = lipgloss.NewStyle().Bold(true).Foreground(orange)
	sBig    = lipgloss.NewStyle().Bold(true)
	sMuted  = lipgloss.NewStyle().Foreground(muted)
	sSpark  = lipgloss.NewStyle().Foreground(orange)
	sHeader = lipgloss.NewStyle().Bold(true).Reverse(true)
	sTabOn  = lipgloss.NewStyle().Bold(true).Foreground(orange).Underline(true)
	sTabOff = lipgloss.NewStyle().Foreground(muted)
	sErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F04438"))
)

func (m model) View() string {
	var b strings.Builder
	f := m.frame

	title := sTitle.Render("ampere top")
	if !m.have {
		b.WriteString(title + "  " + sMuted.Render(fmt.Sprintf("measuring the first %s…", m.interval)) + "\n")
		if m.err != nil {
			b.WriteString(sErr.Render("error: "+m.err.Error()) + "\n")
		}
		return b.String()
	}

	// Machine summary.
	fmt.Fprintf(&b, "%s  %s %s  %s\n", title,
		sMuted.Render(f.Device+" via "+f.Source+","),
		sMuted.Render(fmt.Sprintf("every %s", m.interval)),
		sMuted.Render(f.At.Local().Format("15:04:05")))
	fmt.Fprintf(&b, "%s %s   active %s   total %s   %s\n",
		sMuted.Render("power"),
		sBig.Render(fmt.Sprintf("%7.2f W", f.PowerW)),
		sBig.Render(fmt.Sprintf("%5.1f%%", 100*f.ActiveShare)),
		sBig.Render(formatJ(f.TotalJ)),
		sSpark.Render(sparkline(m.history)))
	b.WriteString(zonesLine(f.Zones) + "\n")
	if m.err != nil {
		b.WriteString(sErr.Render("last sample: "+m.err.Error()) + "\n")
	} else {
		b.WriteString(sMuted.Render("watts and joules are each target's share of the active energy; ENERGY% is of the machine's total") + "\n")
	}
	b.WriteString("\n")

	// Tabs, with "shown/all" counts while searching.
	count := func(rows []Row) string {
		if m.search == "" {
			return fmt.Sprint(len(rows))
		}
		return fmt.Sprintf("%d/%d", len(filter(rows, m.search)), len(rows))
	}
	tabs := []string{
		fmt.Sprintf("[p] processes (%s)", count(f.Processes)),
		fmt.Sprintf("[c] containers (%s)", count(f.Containers)),
	}
	for i, t := range tabs {
		if i == m.tab {
			tabs[i] = sTabOn.Render(t)
		} else {
			tabs[i] = sTabOff.Render(t)
		}
	}
	b.WriteString(strings.Join(tabs, "   ") + "\n")
	b.WriteString(m.searchLine() + "\n")

	// Table.
	rows := m.rows()
	var header string
	if m.tab == tabContainers {
		header = fmt.Sprintf("%-12s  %-24s  %-10s  %7s  %9s  %10s  %7s", "CONTAINER", "NAME", "RUNTIME", "CPU", "POWER", "ENERGY", "ENERGY%")
	} else {
		header = fmt.Sprintf("%7s  %-16s  %7s  %9s  %10s  %7s", "PID", "COMMAND", "CPU", "POWER", "ENERGY", "ENERGY%")
	}
	b.WriteString(sHeader.Render(pad(header, m.width)) + "\n")

	if len(rows) == 0 {
		msg := "no processes yet"
		switch {
		case m.search != "":
			msg = fmt.Sprintf("nothing matches %q", m.search)
		case m.tab == tabContainers:
			msg = "no running docker, podman or containerd containers"
		}
		b.WriteString(sMuted.Render(msg) + "\n")
	}
	end := min(len(rows), m.offset+m.tableHeight())
	for _, r := range rows[m.offset:end] {
		if m.tab == tabContainers {
			fmt.Fprintf(&b, "%-12s  %-24s  %-10s  %6.1f%%  %7.3f W  %10s  %6.1f%%\n",
				shortID(r.Container), truncate(dash(r.Name), 24), r.Runtime, 100*r.CPU, r.W, formatJ(r.TotalJ), energyPct(r, f))
		} else {
			fmt.Fprintf(&b, "%7d  %-16s  %6.1f%%  %7.3f W  %10s  %6.1f%%\n",
				r.PID, truncate(r.Comm, 16), 100*r.CPU, r.W, formatJ(r.TotalJ), energyPct(r, f))
		}
	}

	keys := "↑/↓ scroll   / search   p/c/tab switch   q quit"
	if m.typing {
		keys = "type to search   enter keep   esc cancel"
	} else if m.search != "" {
		keys = "↑/↓ scroll   / edit search   esc clear   q quit"
	}
	b.WriteString(sMuted.Render(fmt.Sprintf("\n%d–%d of %d   %s",
		min(m.offset+1, len(rows)), end, len(rows), keys)))
	return b.String()
}

// energyPct is the row's energy since start as a percentage of the
// machine's energy over the same time (0 if the machine used none yet).
func energyPct(r Row, f Frame) float64 {
	if f.TotalJ <= 0 {
		return 0
	}
	return 100 * r.TotalJ / f.TotalJ
}

// searchLine shows the search being typed ("/ fire▌"), the search in use,
// or nothing.
func (m model) searchLine() string {
	switch {
	case m.typing:
		return sTitle.Render("/ ") + m.search + sSpark.Render("▌")
	case m.search != "":
		return sMuted.Render("search: ") + m.search + sMuted.Render("   (esc clears)")
	}
	return ""
}

// dash returns s, or "-" if s is empty.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// zonesLine lists each zone's power on one line, e.g.
// "package-0 14.20 W · core 6.10 W". Subzones are marked with their parent.
func zonesLine(zones []ZoneRow) string {
	if len(zones) == 0 {
		return sMuted.Render("no zones")
	}
	parts := make([]string, len(zones))
	for i, z := range zones {
		name := z.Name
		if z.Parent != "" {
			name = "└" + name
		}
		parts[i] = fmt.Sprintf("%s %s", sMuted.Render(name), fmt.Sprintf("%.2f W", z.W))
	}
	return strings.Join(parts, sMuted.Render(" · "))
}

// sparkline draws values as block characters, scaled to the largest.
func sparkline(values []float64) string {
	const blocks = "▁▂▃▄▅▆▇█"
	levels := []rune(blocks)
	peak := 0.0
	for _, v := range values {
		peak = max(peak, v)
	}
	var b strings.Builder
	for _, v := range values {
		i := 0
		if peak > 0 {
			i = int(v / peak * float64(len(levels)-1))
		}
		b.WriteRune(levels[max(0, min(i, len(levels)-1))])
	}
	return b.String()
}

// formatJ shows joules, switching to kJ above 10 000 J.
func formatJ(j float64) string {
	if j >= 10000 {
		return fmt.Sprintf("%.2f kJ", j/1000)
	}
	return fmt.Sprintf("%.2f J", j)
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// truncate shortens s to n characters, ending in "…" if cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// pad fills s with spaces to width w, so a reversed header spans the line.
func pad(s string, w int) string {
	if n := w - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}
