package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func testFrame(nProcs int) Frame {
	f := Frame{
		At: time.Date(2026, 11, 21, 10, 0, 1, 0, time.UTC), IntervalS: 1,
		Device: "cpu", Source: "rapl", PowerW: 14.2, ActiveShare: 0.31, TotalJ: 120,
		Zones: []ZoneRow{
			{ID: "intel-rapl:0", Name: "package-0", W: 14.2},
			{ID: "intel-rapl:0:0", Name: "core", Parent: "intel-rapl:0", W: 6.1},
		},
		Containers: []Row{{Container: strings.Repeat("ab", 32), Runtime: "podman", CPU: 0.05, W: 0.22, TotalJ: 3}},
	}
	for i := 0; i < nProcs; i++ {
		f.Processes = append(f.Processes, Row{PID: 100 + i, Comm: fmt.Sprintf("proc-%d", i), CPU: 0.1, W: float64(nProcs - i), TotalJ: 1})
	}
	return f
}

func update(m model, msg tea.Msg) model {
	n, _ := m.Update(msg)
	return n.(model)
}

func key(s string) tea.KeyMsg {
	switch s {
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestViewBeforeFirstFrame(t *testing.T) {
	m := newModel(nil, time.Second)
	if v := m.View(); !strings.Contains(v, "measuring the first 1s") {
		t.Errorf("view = %q", v)
	}
}

func TestViewProcesses(t *testing.T) {
	m := update(newModel(nil, time.Second), frameMsg{f: testFrame(3), ok: true})
	v := m.View()
	for _, want := range []string{
		"ampere top", "14.20 W", "31.0%", "120.00 J",
		"package-0", "core", "6.10 W",
		"[p] processes (3)", "[c] containers (1)",
		"PID", "COMMAND", "POWER", "ENERGY",
		"proc-0", "proc-2", "3.000 W",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
	if strings.Index(v, "proc-0") > strings.Index(v, "proc-2") {
		t.Errorf("rows should keep the frame's order (highest power first):\n%s", v)
	}
}

func TestViewContainersTab(t *testing.T) {
	m := update(newModel(nil, time.Second), frameMsg{f: testFrame(1), ok: true})
	m = update(m, key("c"))
	v := m.View()
	for _, want := range []string{"CONTAINER", "RUNTIME", "abababababab", "podman", "0.220 W"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "abababababababab") {
		t.Errorf("container ID should be shortened to 12 characters:\n%s", v)
	}
	if m = update(m, key("tab")); m.tab != tabProcesses {
		t.Error("tab should switch back to processes")
	}
}

func TestScrolling(t *testing.T) {
	m := update(newModel(nil, time.Second), tea.WindowSizeMsg{Width: 80, Height: 15}) // 4 rows fit
	m = update(m, frameMsg{f: testFrame(10), ok: true})

	if h := m.tableHeight(); h != 4 {
		t.Fatalf("tableHeight = %d, want 4", h)
	}
	m = update(m, key("down"))
	m = update(m, key("j"))
	if m.offset != 2 {
		t.Errorf("offset = %d, want 2", m.offset)
	}
	for i := 0; i < 20; i++ {
		m = update(m, key("down"))
	}
	if m.offset != 6 { // 10 rows, 4 visible
		t.Errorf("offset = %d, want 6 (last page)", m.offset)
	}
	v := m.View()
	if !strings.Contains(v, "proc-9") || strings.Contains(v, "proc-5 ") || !strings.Contains(v, "7–10 of 10") {
		t.Errorf("last page view:\n%s", v)
	}
	if m = update(m, key("g")); m.offset != 0 {
		t.Errorf("g should go to the top, offset %d", m.offset)
	}
}

func TestErrorsAndHistory(t *testing.T) {
	m := update(newModel(nil, time.Second), frameMsg{f: testFrame(1), ok: true})
	m = update(m, frameMsg{err: errors.New("energy counter wrapped")})
	if !strings.Contains(m.View(), "last sample: energy counter wrapped") || !m.have {
		t.Errorf("an error keeps the last frame and shows the error:\n%s", m.View())
	}
	for i := 0; i < historyLen+5; i++ {
		m = update(m, frameMsg{f: testFrame(1), ok: true})
	}
	if len(m.history) != historyLen || m.err != nil {
		t.Errorf("history %d, err %v", len(m.history), m.err)
	}
}

func TestQuit(t *testing.T) {
	for _, k := range []string{"q", "esc"} {
		var msg tea.Msg = key(k)
		if k == "esc" {
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		}
		_, cmd := newModel(nil, time.Second).Update(msg)
		if cmd == nil {
			t.Fatalf("%s: no command", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%s should quit", k)
		}
	}
}

func TestHelpers(t *testing.T) {
	if s := sparkline([]float64{0, 5, 10}); s != "▁▄█" {
		t.Errorf("sparkline = %q", s)
	}
	if s := sparkline([]float64{0, 0}); s != "▁▁" {
		t.Errorf("flat sparkline = %q", s)
	}
	if formatJ(12.345) != "12.35 J" || formatJ(12345) != "12.35 kJ" {
		t.Errorf("formatJ: %q %q", formatJ(12.345), formatJ(12345))
	}
	if truncate("a-very-long-command-name", 8) != "a-very-…" || truncate("short", 8) != "short" {
		t.Error("truncate")
	}
}

// typeText sends each character as a key press.
func typeText(m model, s string) model {
	for _, r := range s {
		m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func searchFrame() Frame {
	f := testFrame(0)
	f.Processes = []Row{
		{PID: 4211, Comm: "firefox", W: 3},
		{PID: 913, Comm: "containerd", W: 2},
		{PID: 42, Comm: "Xorg", W: 1},
	}
	f.Containers = []Row{
		{Container: "44f86868889b" + strings.Repeat("0", 52), Name: "k8s-101-worker", Runtime: "docker", W: 2},
		{Container: "eb827e1203f2" + strings.Repeat("0", 52), Name: "k8s-101-control-plane", Runtime: "docker", W: 1},
		{Container: "9c1d2e3f4a5b" + strings.Repeat("0", 52), Name: "", Runtime: "podman", W: 0.5},
	}
	return f
}

func TestSearchProcesses(t *testing.T) {
	m := update(newModel(nil, time.Second), frameMsg{f: searchFrame(), ok: true})

	m = update(m, key("/"))
	if !m.typing {
		t.Fatal("/ should start typing a search")
	}
	m = typeText(m, "FIRE") // case does not matter
	v := m.View()
	if !strings.Contains(v, "/ FIRE") || !strings.Contains(v, "firefox") ||
		strings.Contains(v, "containerd") || !strings.Contains(v, "processes (1/3)") {
		t.Errorf("search FIRE:\n%s", v)
	}

	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.typing || m.search != "FIRE" || !strings.Contains(m.View(), "search: FIRE") {
		t.Errorf("enter keeps the search: typing %v, search %q", m.typing, m.search)
	}

	// By PID prefix.
	m.search = "91"
	if rows := m.rows(); len(rows) != 1 || rows[0].Comm != "containerd" {
		t.Errorf("PID 91…: %+v", rows)
	}
}

func TestSearchContainers(t *testing.T) {
	m := update(newModel(nil, time.Second), frameMsg{f: searchFrame(), ok: true})
	m = update(m, key("c"))

	for q, want := range map[string]int{
		"worker":  1, // name
		"k8s-101": 2, // part of two names
		"eb827e":  1, // ID prefix
		"podman":  1, // runtime
		"nothing": 0,
	} {
		m.search = q
		if got := len(m.rows()); got != want {
			t.Errorf("search %q: %d rows, want %d", q, got, want)
		}
	}

	m.search = ""
	v := m.View()
	for _, want := range []string{"NAME", "k8s-101-worker", "k8s-101-control-plane", "44f86868889b"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}

	m.search = "nothing"
	if v := m.View(); !strings.Contains(v, `nothing matches "nothing"`) || !strings.Contains(v, "containers (0/3)") {
		t.Errorf("no match view:\n%s", v)
	}
}

func TestSearchKeys(t *testing.T) {
	m := update(newModel(nil, time.Second), frameMsg{f: searchFrame(), ok: true})

	// While typing, q, p, c and j are text, not commands.
	m = typeText(update(m, key("/")), "qpcj")
	if m.search != "qpcj" || m.tab != tabProcesses {
		t.Errorf("typing: search %q, tab %d", m.search, m.tab)
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.search != "qpc" {
		t.Errorf("backspace: search %q", m.search)
	}

	// Esc while typing cancels the search.
	m = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.typing || m.search != "" {
		t.Errorf("esc while typing: typing %v, search %q", m.typing, m.search)
	}

	// Esc with a kept search clears it instead of quitting.
	m = update(typeText(update(m, key("/")), "fire"), tea.KeyMsg{Type: tea.KeyEnter})
	n, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m = n.(model); m.search != "" || cmd != nil {
		t.Errorf("esc with a search should clear it, not quit: search %q", m.search)
	}

	// Ctrl+C always quits, even while typing.
	_, cmd = update(m, key("/")).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c: no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("ctrl+c should quit while typing")
	}
}

func TestSearchResetsScroll(t *testing.T) {
	m := update(newModel(nil, time.Second), tea.WindowSizeMsg{Width: 80, Height: 15})
	m = update(m, frameMsg{f: testFrame(10), ok: true})
	for i := 0; i < 5; i++ {
		m = update(m, key("down"))
	}
	m = typeText(update(m, key("/")), "proc-9")
	if m.offset != 0 || len(m.rows()) != 1 || !strings.Contains(m.View(), "proc-9") {
		t.Errorf("search should go back to the first row: offset %d, rows %d", m.offset, len(m.rows()))
	}
}

func TestEnergyPercent(t *testing.T) {
	f := testFrame(0) // machine total 120 J
	f.Processes = []Row{{PID: 1, Comm: "busy", W: 2, TotalJ: 30}, {PID: 2, Comm: "idle", TotalJ: 0}}
	f.Containers = []Row{{Container: strings.Repeat("a", 64), Runtime: "docker", TotalJ: 12}}
	m := update(newModel(nil, time.Second), frameMsg{f: f, ok: true})

	v := m.View()
	if !strings.Contains(v, "ENERGY%") || !strings.Contains(v, "25.0%") || !strings.Contains(v, "0.0%") {
		t.Errorf("busy used 30 of 120 J = 25.0%%:\n%s", v)
	}
	if v := update(m, key("c")).View(); !strings.Contains(v, "ENERGY%") || !strings.Contains(v, "10.0%") {
		t.Errorf("container used 12 of 120 J = 10.0%%:\n%s", v)
	}
	if energyPct(Row{TotalJ: 5}, Frame{}) != 0 {
		t.Error("no machine energy yet: 0%")
	}
}
