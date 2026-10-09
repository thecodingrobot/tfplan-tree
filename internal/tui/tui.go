// Package tui implements the interactive tree viewer.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ns/terraform-plan-preview/internal/planfile"
	"github.com/ns/terraform-plan-preview/internal/tree"
)

var (
	stBranch    = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	stArrow     = lipgloss.NewStyle().Foreground(lipgloss.Color("37"))
	stModule    = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	stSection   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	stData      = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	stDim       = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	stCursor    = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	stCursorMod = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("81")).Bold(true)
	stMark      = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	stStatus    = lipgloss.NewStyle().Foreground(lipgloss.Color("71"))
	stErr       = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	stQuery     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	stTitle     = lipgloss.NewStyle().Bold(true)

	actColor = map[string]lipgloss.Style{
		"create":  lipgloss.NewStyle().Foreground(lipgloss.Color("46")),
		"update":  lipgloss.NewStyle().Foreground(lipgloss.Color("226")),
		"delete":  lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
		"replace": lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true),
		"read":    lipgloss.NewStyle().Foreground(lipgloss.Color("39")),
		"forget":  lipgloss.NewStyle().Foreground(lipgloss.Color("141")),
		"noop":    lipgloss.NewStyle().Foreground(lipgloss.Color("243")),
	}
	actSym = map[string]string{
		"create": "+", "update": "~", "delete": "-", "replace": "±",
		"read": "→", "forget": "⊗", "noop": "=",
	}
	actOrder = []string{"create", "replace", "update", "delete", "read", "forget", "noop"}
)

type row struct {
	node    *tree.Node
	depth   int
	guides  []bool // per ancestor level: true → draw │ continuation
	key     string // unique mark key (section\x00addr)
	section string
}

type Model struct {
	plan      *planfile.Plan
	showAll   bool
	query     string
	searching bool
	searchIn  string
	collapsed map[string]bool
	marked    map[string]bool
	cursor    int
	rows      []row
	viewTop   int
	width     int
	height    int
	status    string
	statusErr bool
	help      bool
	cmdName   string // binary name for status messages
}

func New(p *planfile.Plan) Model {
	m := Model{
		plan:      p,
		collapsed: map[string]bool{},
		marked:    map[string]bool{},
		cmdName:   "tfplan-tree",
	}
	m.rebuild()
	return m
}

func (m *Model) rebuild() {
	inc := tree.DefaultInclude
	if m.showAll {
		inc = tree.AllInclude
	}
	roots := tree.Build(m.plan, m.plan.Changes, m.plan.Drift, inc)
	q := strings.ToLower(m.query)
	m.rows = flatten(roots, q, m.collapsed)
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.clampView()
}

func flatten(roots []*tree.Node, query string, collapsed map[string]bool) []row {
	var out []row
	var walk func(nodes []*tree.Node, depth int, guides []bool, section string)
	walk = func(nodes []*tree.Node, depth int, guides []bool, section string) {
		for i, n := range nodes {
			last := i == len(nodes)-1
			key := section + "\x00" + n.Addr
			r := row{node: n, depth: depth, key: key, section: section}
			r.guides = append(append([]bool{}, guides...), last)
			out = append(out, r)
			if n.Kind == tree.KindSection {
				section = n.Label
			}
			if len(n.Children) > 0 && !(collapsed[n.Addr] && n.Addr != "") {
				g := append(append([]bool{}, guides...), last)
				walk(n.Children, depth+1, g, section)
			}
		}
	}
	if query == "" {
		walk(roots, 0, nil, "")
		return out
	}
	// query mode: keep nodes whose subtree matches; prune everything else
	var matches func(n *tree.Node) bool
	matches = func(n *tree.Node) bool {
		if strings.Contains(strings.ToLower(n.Addr+" "+n.Label), query) {
			return true
		}
		for _, c := range n.Children {
			if matches(c) {
				return true
			}
		}
		return false
	}
	var prune func(n *tree.Node) *tree.Node
	prune = func(n *tree.Node) *tree.Node {
		if strings.Contains(strings.ToLower(n.Addr+" "+n.Label), query) {
			return n // self matches: keep whole subtree
		}
		var kids []*tree.Node
		for _, c := range n.Children {
			if p := prune(c); p != nil {
				kids = append(kids, p)
			}
		}
		if len(kids) == 0 {
			return nil
		}
		n.Children = kids // safe: the tree is rebuilt on every rebuild()
		return n
	}
	var kept []*tree.Node
	for _, n := range roots {
		if p := prune(n); p != nil {
			kept = append(kept, p)
		}
	}
	// ignore collapsed state while filtering so all matches are visible
	collapsed = nil
	walk(kept, 0, nil, "")
	return out
}

func (m *Model) clampView() {
	_, h := m.width, m.height
	body := m.bodyHeight()
	if m.viewTop > m.cursor {
		m.viewTop = m.cursor
	}
	if m.cursor >= m.viewTop+body && body > 0 {
		m.viewTop = m.cursor - body + 1
	}
	_ = h
}

func (m *Model) bodyHeight() int {
	h := m.height - headerLines*2 - 4 // header+blank, footer(status+keys)
	if m.help {
		h -= helpLines
	}
	if h < 1 {
		h = 1
	}
	return h
}

const headerLines = 1
const helpLines = 12

func (m *Model) setStatus(s string, isErr bool) {
	m.status = s
	m.statusErr = isErr
}

// targets returns the addresses to copy: marked rows if any, else cursor row.
func (m *Model) targets() []string {
	var addrs []string
	seen := map[string]bool{}
	for _, r := range m.rows {
		if m.marked[r.key] && r.node.Addr != "" && !seen[r.node.Addr] {
			addrs = append(addrs, r.node.Addr)
			seen[r.node.Addr] = true
		}
	}
	if len(addrs) == 0 && m.cursor >= 0 && m.cursor < len(m.rows) {
		n := m.rows[m.cursor].node
		if n.Addr != "" {
			addrs = []string{n.Addr}
		}
	}
	return addrs
}

func (m *Model) copyCmd(verb string) {
	addrs := m.targets()
	if len(addrs) == 0 {
		m.setStatus("nothing to copy here — select a module or resource", true)
		return
	}
	var cmd string
	if verb == "addrs" {
		cmd = strings.Join(addrs, " ")
	} else {
		var parts []string
		for _, a := range addrs {
			parts = append(parts, "-target='"+a+"'")
		}
		cmd = "terraform " + verb + " " + strings.Join(parts, " ")
	}
	if err := Copy(cmd); err != nil {
		m.setStatus("clipboard unavailable — "+cmd, true)
		return
	}
	m.setStatus("copied ✓ "+cmd, false)
}

func (m *Model) toggleMark(subtree bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	r := m.rows[m.cursor]
	if r.node.Addr == "" {
		return
	}
	if !subtree {
		if m.marked[r.key] {
			delete(m.marked, r.key)
		} else {
			m.marked[r.key] = true
		}
		return
	}
	// mark/unmark the whole subtree under (and including) this row
	var keys []string
	for i := m.cursor; i < len(m.rows); i++ {
		rr := m.rows[i]
		if rr.key == r.key {
			keys = append(keys, rr.key)
			continue
		}
		if rr.depth <= r.depth {
			break
		}
		keys = append(keys, rr.key)
	}
	allMarked := true
	for _, k := range keys {
		if !m.marked[k] {
			allMarked = false
			break
		}
	}
	for _, k := range keys {
		if allMarked {
			delete(m.marked, k)
		} else {
			m.marked[k] = true
		}
	}
}

func (m *Model) setExpandAll(expand bool) {
	if expand {
		m.collapsed = map[string]bool{}
	} else {
		for _, r := range m.rows {
			if len(r.node.Children) > 0 && r.node.Addr != "" {
				m.collapsed[r.node.Addr] = true
			}
		}
	}
	m.rebuild()
}

func (m *Model) expandUnderCursor(expand bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	r := m.rows[m.cursor]
	if len(r.node.Children) == 0 {
		return
	}
	var apply func(n *tree.Node)
	apply = func(n *tree.Node) {
		if n.Addr != "" {
			if expand {
				delete(m.collapsed, n.Addr)
			} else {
				m.collapsed[n.Addr] = true
			}
		}
		for _, c := range n.Children {
			apply(c)
		}
	}
	apply(r.node)
	m.rebuild()
}

func (m *Model) move(d int) {
	m.cursor += d
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.clampView()
}

func (m *Model) jumpToTop()    { m.cursor = 0; m.clampView() }
func (m *Model) jumpToBottom() { m.cursor = len(m.rows) - 1; m.clampView() }

func (m *Model) page(delta int) {
	m.move(delta * max(1, m.bodyHeight()-1))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---- tea.Model ----

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampView()
	case tea.KeyMsg:
		if m.searching {
			return m.updateSearch(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c":
			if m.help {
				m.help = false
				return m, nil
			}
			return m, tea.Quit
		case "esc":
			if m.query != "" {
				m.query = ""
				m.searchIn = ""
				m.rebuild()
				m.setStatus("filter cleared", false)
			}
		case "?":
			m.help = !m.help
			m.clampView()
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "pgup":
			m.page(-1)
		case "pgdown":
			m.page(1)
		case "g", "home":
			m.jumpToTop()
		case "G", "end":
			m.jumpToBottom()
		case "right", "l", "enter":
			if m.cursor < len(m.rows) {
				r := m.rows[m.cursor]
				if len(r.node.Children) > 0 {
					delete(m.collapsed, r.node.Addr)
					m.rebuild()
				}
			}
		case "left", "h":
			if m.cursor < len(m.rows) {
				r := m.rows[m.cursor]
				if len(r.node.Children) > 0 && r.node.Addr != "" {
					m.collapsed[r.node.Addr] = true
					m.rebuild()
				} else {
					// jump to parent
					for i := m.cursor - 1; i >= 0; i-- {
						if m.rows[i].depth < r.depth {
							m.cursor = i
							m.clampView()
							break
						}
					}
				}
			}
		case "e":
			m.expandUnderCursor(true)
		case "C":
			m.setExpandAll(false)
		case "E":
			m.setExpandAll(true)
		case " ":
			m.toggleMark(false)
		case "a":
			m.toggleMark(true)
		case "A":
			m.marked = map[string]bool{}
			m.setStatus("marks cleared", false)
		case "y", "c":
			m.copyCmd("apply")
		case "d":
			m.copyCmd("destroy")
		case "Y":
			m.copyCmd("addrs")
		case "n":
			m.showAll = !m.showAll
			m.rebuild()
			if m.showAll {
				m.setStatus("showing unchanged/data-read resources too", false)
			} else {
				m.setStatus("", false)
			}
		case "/":
			m.searching = true
			m.searchIn = ""
			return m, nil
		case "tab":
			// jump between marked rows
			if len(m.rows) > 0 {
				for i := 1; i <= len(m.rows); i++ {
					j := (m.cursor + i) % len(m.rows)
					if m.marked[m.rows[j].key] {
						m.cursor = j
						m.clampView()
						break
					}
				}
			}
		}
	}
	return m, nil
}

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.searching = false
		m.query = m.searchIn
		m.rebuild()
		m.jumpToTop()
		if len(m.rows) == 0 {
			m.setStatus("no matches for "+m.query, true)
		} else {
			m.setStatus(m.queryHint(), false)
		}
	case "esc":
		m.searching = false
		m.searchIn = ""
		m.query = ""
		m.rebuild()
	case "backspace":
		if len(m.searchIn) > 0 {
			m.searchIn = m.searchIn[:len(m.searchIn)-1]
		}
	default:
		if len(msg.Runes) > 0 {
			m.searchIn += string(msg.Runes)
		}
	}
	return m, nil
}

func (m Model) queryHint() string {
	if m.query == "" {
		return ""
	}
	return "filter: " + m.query + "  (/+enter to edit, esc to clear)"
}

func (m Model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n\n")

	body := m.bodyHeight()
	top := m.viewTop
	end := top + body
	if end > len(m.rows) {
		end = len(m.rows)
	}
	for i := top; i < end; i++ {
		b.WriteString(m.renderRow(m.rows[i], i == m.cursor))
		b.WriteString("\n")
	}
	for i := end - top; i < body; i++ {
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.statusLine())
	b.WriteString("\n")
	b.WriteString(m.footerKeys())
	if m.help {
		b.WriteString("\n" + m.helpText())
	}
	return b.String()
}

func (m Model) header() string {
	total := len(m.plan.Changes)
	counts := map[string]int{}
	for _, rc := range m.plan.Changes {
		counts[rc.Action.Kind()]++
	}
	badges := m.badgeStr(counts)
	hidden := counts["noop"] + countDataReads(m.plan)
	title := stTitle.Render(filepathBase(m.plan.Path))
	meta := []string{title}
	if m.plan.TerraformVersion != "" {
		meta = append(meta, "terraform v"+m.plan.TerraformVersion)
	}
	if m.plan.Errored {
		meta = append(meta, stErr.Render("plan ERRORED"))
	}
	meta = append(meta, fmt.Sprintf("%d resources: %s", total, badges))
	if hidden > 0 && !m.showAll {
		meta = append(meta, stDim.Render(fmt.Sprintf("(%d unchanged hidden)", hidden)))
	}
	s := strings.Join(meta, "  ·  ")
	return truncateANSI(s, m.width)
}

func countDataReads(p *planfile.Plan) int {
	n := 0
	for _, rc := range p.Changes {
		if planfile.IsData(rc.Addr) && rc.Action.Kind() == "read" {
			n++
		}
	}
	return n
}

func (m Model) badgeStr(counts map[string]int) string {
	var parts []string
	for _, k := range actOrder {
		if counts[k] > 0 {
			parts = append(parts, actColor[k].Render(fmt.Sprintf("%s%d", actSym[k], counts[k])))
		}
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, " ")
}

func (m Model) statusLine() string {
	if m.searching {
		return stQuery.Render("/"+m.searchIn) + "▏"
	}
	if m.status == "" {
		if h := m.queryHint(); h != "" {
			return stDim.Render(h)
		}
		return ""
	}
	if m.statusErr {
		return stErr.Render(truncateANSI(m.status, m.width))
	}
	return stStatus.Render(truncateANSI(m.status, m.width))
}

func (m Model) footerKeys() string {
	if m.help {
		return stDim.Render("┈┈ keys ──── ? close help ──── q quit ┈┈")
	}
	return stDim.Render("j/k move  ←→ collapse/expand  space mark  a mark subtree  y copy apply cmd  Y copy addrs  d destroy  / filter  tab next mark  ? help  q quit")
}

func (m Model) helpText() string {
	lines := []string{
		"  j / k, ↑ / ↓      move cursor            g / G      top / bottom",
		"  → / l / enter     expand node            ← / h      collapse / go to parent",
		"  e                 expand subtree         E / C      expand / collapse all",
		"  space             mark single target     a          mark whole subtree",
		"  A                 clear all marks        tab        jump to next mark",
		"  y or c            copy `terraform apply -target=…` (marked or cursor)",
		"  d                 copy `terraform destroy -target=…`",
		"  Y                 copy bare addresses (space separated)",
		"  /                 filter tree by substring   esc    clear filter",
		"  n                 toggle unchanged / data-read resources",
		"  ?                 toggle this help       q         quit",
		"",
		"  Marking a module node copies the module address, which targets",
		"  everything inside it. Copying uses OSC52 + local clipboard tools.",
	}
	return stDim.Render(strings.Join(lines, "\n"))
}

func (m Model) renderRow(r row, selected bool) string {
	n := r.node
	var b strings.Builder
	for i := 0; i < len(r.guides)-1; i++ {
		if r.guides[i] {
			b.WriteString("   ")
		} else {
			b.WriteString(stBranch.Render("│  "))
		}
	}
	if r.depth > 0 {
		if r.guides[len(r.guides)-1] {
			b.WriteString(stBranch.Render("└─ "))
		} else {
			b.WriteString(stBranch.Render("├─ "))
		}
	}

	mark := " "
	if m.marked[r.key] {
		mark = stMark.Render("●")
	}
	b.WriteString(mark)
	b.WriteString(" ")

	switch n.Kind {
	case tree.KindSection:
		b.WriteString(stArrow.Render("▾ "))
		b.WriteString(stSection.Render(n.Label))
	case tree.KindModule:
		arrow := "▾"
		if m.collapsed[n.Addr] {
			arrow = "▸"
		}
		b.WriteString(stArrow.Render(arrow + " "))
		b.WriteString(stModule.Render(n.Label))
		b.WriteString("  ")
		b.WriteString(m.badgeStr(n.Counts))
	default:
		b.WriteString(actionBadge(n.Change.Action))
		label := n.Label
		style := lipgloss.NewStyle()
		if planfile.IsData(n.Addr) {
			style = stData
		}
		b.WriteString(style.Render(label))
		if n.Change != nil {
			if n.Change.DeposedKey != "" {
				b.WriteString(stDim.Render(" (deposed)"))
			}
			switch n.Change.ActionReason {
			case 1:
				b.WriteString(stDim.Render(" (tainted)"))
			case 3:
				b.WriteString(stDim.Render(" (cannot update in-place)"))
			case 9:
				b.WriteString(stDim.Render(" (triggered replacement)"))
			}
		}
	}

	line := b.String()
	if selected {
		if n.Kind == tree.KindModule || n.Kind == tree.KindSection {
			line = stCursorMod.Render(pad(line, m.width))
		} else {
			line = stCursor.Render(pad(line, m.width))
		}
	}
	return line
}

func actionBadge(a planfile.Action) string {
	st := actColor[a.Kind()]
	return st.Render(fmt.Sprintf("%-2s", a.Short()))
}

func pad(s string, w int) string {
	width := lipgloss.Width(s)
	if width >= w {
		return s
	}
	return s + strings.Repeat(" ", w-width)
}

func truncateANSI(s string, w int) string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return s
	}
	// simple visible-char truncation preserving escapes is complex; use runes of stripped check
	var b strings.Builder
	width := 0
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			b.WriteRune(r)
			continue
		}
		if inEsc {
			b.WriteRune(r)
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if width >= w-1 {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
		width++
	}
	return b.String()
}

func filepathBase(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Run starts the interactive UI.
func Run(p *planfile.Plan) error {
	_, err := tea.NewProgram(New(p), tea.WithAltScreen()).Run()
	return err
}

// PrintStatic renders the tree without interaction (non-TTY mode).
func PrintStatic(p *planfile.Plan, all bool, flat bool) {
	inc := tree.DefaultInclude
	if all {
		inc = tree.AllInclude
	}
	roots := tree.Build(p, p.Changes, p.Drift, inc)

	if flat {
		var walk func(nodes []*tree.Node)
		walk = func(nodes []*tree.Node) {
			for _, n := range nodes {
				if n.Kind == tree.KindResource {
					fmt.Println(n.Addr)
				}
				walk(n.Children)
			}
		}
		walk(roots)
		return
	}

	counts := map[string]int{}
	for _, rc := range p.Changes {
		counts[rc.Action.Kind()]++
	}
	m := Model{plan: p, showAll: all, collapsed: map[string]bool{}, marked: map[string]bool{}, width: 120, height: 1000}
	fmt.Println(m.header())
	fmt.Println()
	var render func(nodes []*tree.Node, depth int, guides []bool)
	render = func(nodes []*tree.Node, depth int, guides []bool) {
		for i, n := range nodes {
			last := i == len(nodes)-1
			r := row{node: n, depth: depth}
			r.guides = append(append([]bool{}, guides...), last)
			fmt.Println(m.renderRow(r, false))
			if len(n.Children) > 0 {
				g := append(append([]bool{}, guides...), last)
				render(n.Children, depth+1, g)
			}
		}
	}
	render(roots, 0, nil)
	fmt.Println()
	fmt.Println("copy a target:  terraform apply -target='<address above>'")
}
