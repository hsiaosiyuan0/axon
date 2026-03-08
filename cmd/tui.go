package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/hybrid"
	"github.com/hsiaosiyuan0/axon/internal/rerank"
	"github.com/hsiaosiyuan0/axon/internal/store"
	"github.com/spf13/cobra"
)

// ── styles ────────────────────────────────────────────────────────────────────

var (
	styleTitleBar = lipgloss.NewStyle().
			Background(lipgloss.Color("62")).
			Foreground(lipgloss.Color("230")).
			Bold(true).
			Padding(0, 2)

	stylePrompt = lipgloss.NewStyle().
			Foreground(lipgloss.Color("62")).
			Bold(true)

	styleSelected = lipgloss.NewStyle().
			Background(lipgloss.Color("237")).
			Foreground(lipgloss.Color("255"))

	styleNormal = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	styleDim = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	styleScore = lipgloss.NewStyle().
			Foreground(lipgloss.Color("72"))

	styleSource = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Italic(true)

	stylePreviewBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(1, 2)

	styleHint = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	styleRerankOn = lipgloss.NewStyle().
			Foreground(lipgloss.Color("82")).
			Bold(true)

	styleRerankOff = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	styleNotice = lipgloss.NewStyle().
			Foreground(lipgloss.Color("220"))

	// collection picker panel styles
	stylePickerBorder = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("62")).
				Padding(0, 1)

	stylePickerSelected = lipgloss.NewStyle().
				Background(lipgloss.Color("62")).
				Foreground(lipgloss.Color("230")).
				Bold(true).
				Padding(0, 1)

	stylePickerNormal = lipgloss.NewStyle().
				Foreground(lipgloss.Color("252")).
				Padding(0, 1)

	styleCollectionTag = lipgloss.NewStyle().
				Foreground(lipgloss.Color("75")).
				Bold(true)

	styleRerankLLM = lipgloss.NewStyle().
			Foreground(lipgloss.Color("213")).
			Bold(true)

	styleRerankToken = lipgloss.NewStyle().
				Foreground(lipgloss.Color("82")).
				Bold(true)

	// thinking panel styles
	styleThinkBorder = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("213")).
				Padding(0, 1)

	styleThinkText = lipgloss.NewStyle().
			Foreground(lipgloss.Color("249"))

	styleThinkHint = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Italic(true)
)

// debounce delay for live search
const debounceDelay = 150 * time.Millisecond

// tuiProgram is a package-level reference to the running tea.Program,
// used by background goroutines (e.g. streaming rerank) to Send messages.
var tuiProgram *tea.Program

// ── messages ──────────────────────────────────────────────────────────────────

type searchDoneMsg struct {
	results []tuiResult
	err     error
}

// debounceTickMsg fires after the debounce timer expires.
type debounceTickMsg struct{ query string }

// noticeMsg clears transient status notices.
type noticeMsg struct{}

// quitCancelMsg cancels the pending quit confirmation.
type quitCancelMsg struct{}

// collectionsLoadedMsg carries the list of collections fetched from DB.
type collectionsLoadedMsg struct {
	collections []store.Collection
	err         error
}

// rerankTokenMsg carries a single streaming token from the LLM reranker.
type rerankTokenMsg struct{ token string }

// rerankBatchMsg signals which batch is being processed (current, total).
type rerankBatchMsg struct{ current, total int }

// rerankDoneMsg carries the final reranked results after streaming completes.
type rerankDoneMsg struct {
	results []tuiResult
	err     error
}

// ── model ─────────────────────────────────────────────────────────────────────

type tuiResult struct {
	content    string
	source     string
	collection string // collection name for display
	score      float64
}

type tuiModel struct {
	cfg        *config.Config
	collection string // active collection name (empty = all)

	input    string
	results  []tuiResult
	selected int
	loading  bool
	err      error
	width    int
	height   int

	mode string // "search" | "preview" | "picker"

	// ① debounce: track the query we last scheduled a search for
	pendingQuery string

	// ② preview scroll
	previewScroll int

	// ③ rerank
	rerankEnabled bool
	rerankMode    string // "token" | "llm"

	// rerank picker cursor
	rerankPickerSelected int

	// ④ transient notice (e.g. "Copied!")
	notice string

	// ⑦ LLM rerank streaming / thinking panel
	rerankStreaming    bool
	rerankThinking     string // accumulated LLM tokens
	rerankThinkExpand  bool   // ctrl+o to expand/collapse
	rerankBatchCurrent int
	rerankBatchTotal   int

	// ⑧ quit confirmation
	quitPending bool

	// ⑥ collection picker
	collections    []store.Collection // all available collections
	pickerSelected int                // cursor in picker list
}

func (m tuiModel) Init() tea.Cmd {
	return m.loadCollections()
}

// loadCollections fetches all collections from the DB asynchronously.
func (m tuiModel) loadCollections() tea.Cmd {
	return func() tea.Msg {
		db, err := store.Open(m.cfg.DBPath)
		if err != nil {
			return collectionsLoadedMsg{err: err}
		}
		defer db.Close()
		cols, err := db.Collections().List()
		return collectionsLoadedMsg{collections: cols, err: err}
	}
}

// scheduleSearch starts the debounce timer for the current input.
func (m tuiModel) scheduleSearch() tea.Cmd {
	q := m.input
	return tea.Tick(debounceDelay, func(_ time.Time) tea.Msg {
		return debounceTickMsg{query: q}
	})
}

// doSearch performs the actual search (called after debounce fires).
func (m tuiModel) doSearch(query string) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(query) == "" {
			return searchDoneMsg{results: nil}
		}
		searcher, err := hybrid.NewSearcher(m.cfg)
		if err != nil {
			return searchDoneMsg{err: err}
		}
		defer searcher.Close()

		res, err := searcher.Search(context.Background(), hybrid.SearchOptions{
			Query:      query,
			Collection: m.collection,
			Limit:      10,
			Rerank:     m.rerankEnabled,
			RerankMode: m.rerankMode,
		})
		if err != nil {
			return searchDoneMsg{err: err}
		}

		var out []tuiResult
		for _, r := range res {
			out = append(out, tuiResult{
				content:    r.Content,
				source:     r.Source,
				collection: r.Collection,
				score:      r.Score,
			})
		}
		return searchDoneMsg{results: out}
	}
}

// doRerankStream performs an LLM-rerank search with streaming token output.
// It first retrieves base search results, then streams LLM reranking.
func (m tuiModel) doRerankStream(query string) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(query) == "" {
			return rerankDoneMsg{results: nil}
		}

		// Step 1: get base results (no rerank yet)
		searcher, err := hybrid.NewSearcher(m.cfg)
		if err != nil {
			return rerankDoneMsg{err: err}
		}
		defer searcher.Close()

		res, err := searcher.Search(context.Background(), hybrid.SearchOptions{
			Query:      query,
			Collection: m.collection,
			Limit:      10,
			Rerank:     false,
		})
		if err != nil {
			return rerankDoneMsg{err: err}
		}

		// Convert to rerank.Candidate
		candidates := make([]rerank.Candidate, len(res))
		for i, r := range res {
			candidates[i] = rerank.Candidate{
				ID:         r.ChunkID,
				Content:    r.Content,
				Source:     r.Source,
				Collection: r.Collection,
				Score:      r.Score,
			}
		}

		// Step 2: stream LLM rerank
		reranker := rerank.NewLLMReranker(m.cfg)
		reranked, err := reranker.RerankStream(
			context.Background(),
			query,
			candidates,
			func(token string) {
				tuiProgram.Send(rerankTokenMsg{token: token})
			},
			func(current, total int) {
				tuiProgram.Send(rerankBatchMsg{current: current, total: total})
			},
		)
		if err != nil {
			return rerankDoneMsg{err: err}
		}

		var out []tuiResult
		for _, r := range reranked {
			out = append(out, tuiResult{
				content:    r.Content,
				source:     r.Source,
				collection: r.Collection,
				score:      r.Score,
			})
		}
		return rerankDoneMsg{results: out}
	}
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	// ① debounce tick: only run search if query hasn't changed since scheduled
	case debounceTickMsg:
		if msg.query == m.input && m.loading {
			if m.rerankEnabled && m.rerankMode == "llm" {
				m.rerankStreaming = true
				m.rerankThinking = ""
				m.rerankThinkExpand = false
				m.rerankBatchCurrent = 0
				m.rerankBatchTotal = 0
				return m, m.doRerankStream(m.input)
			}
			return m, m.doSearch(m.input)
		}
		return m, nil

	case searchDoneMsg:
		m.loading = false
		m.err = msg.err
		m.results = msg.results
		m.selected = 0
		return m, nil

	case rerankTokenMsg:
		m.rerankThinking += msg.token
		return m, nil

	case rerankBatchMsg:
		m.rerankBatchCurrent = msg.current
		m.rerankBatchTotal = msg.total
		return m, nil

	case rerankDoneMsg:
		m.loading = false
		m.rerankStreaming = false
		m.err = msg.err
		m.results = msg.results
		m.selected = 0
		return m, nil

	case noticeMsg:
		m.notice = ""
		return m, nil

	case quitCancelMsg:
		m.quitPending = false
		if m.notice == "Press q again to quit" {
			m.notice = ""
		}
		return m, nil

	// ⑥ collections loaded from DB
	case collectionsLoadedMsg:
		if msg.err == nil {
			m.collections = msg.collections
		}
		return m, nil

	case tea.KeyMsg:
		// ── rerank picker mode ────────────────────────────────────────────
		if m.mode == "rerank_picker" {
			switch msg.String() {
			case "esc", "q":
				m.mode = "search"
			case "up", "ctrl+p", "k":
				if m.rerankPickerSelected > 0 {
					m.rerankPickerSelected--
				}
			case "down", "ctrl+n", "j":
				if m.rerankPickerSelected < 2 { // 0=off 1=token 2=llm
					m.rerankPickerSelected++
				}
			case "enter":
				m.mode = "search"
				switch m.rerankPickerSelected {
				case 0:
					m.rerankEnabled = false
					m.rerankMode = ""
				case 1:
					m.rerankEnabled = true
					m.rerankMode = "token"
				case 2:
					m.rerankEnabled = true
					m.rerankMode = "llm"
				}
				if strings.TrimSpace(m.input) != "" {
					m.loading = true
					m.results = nil
					if m.rerankMode == "llm" {
						m.rerankStreaming = true
						m.rerankThinking = ""
						m.rerankThinkExpand = false
						m.rerankBatchCurrent = 0
						m.rerankBatchTotal = 0
						return m, m.doRerankStream(m.input)
					}
					return m, m.doSearch(m.input)
				}
			}
			return m, nil
		}

		// ── picker mode ───────────────────────────────────────────────────
		if m.mode == "picker" {
			switch msg.String() {
			case "esc", "q":
				m.mode = "search"
			case "up", "ctrl+p", "k":
				if m.pickerSelected > 0 {
					m.pickerSelected--
				}
			case "down", "ctrl+n", "j":
				// +1 for the "All collections" entry at index 0
				if m.pickerSelected < len(m.collections) {
					m.pickerSelected++
				}
			case "enter":
				m.mode = "search"
				if m.pickerSelected == 0 {
					m.collection = ""
				} else {
					m.collection = m.collections[m.pickerSelected-1].Name
				}
				// re-search with new collection if query exists
				if strings.TrimSpace(m.input) != "" {
					m.loading = true
					m.results = nil
					return m, m.doSearch(m.input)
				}
			}
			return m, nil
		}
		// ── preview mode ──────────────────────────────────────────────────
		if m.mode == "preview" {
			switch msg.String() {
			case "esc", "q", "enter":
				m.mode = "search"
				m.previewScroll = 0

			// ② preview scroll
			case "up", "ctrl+p", "k":
				if m.previewScroll > 0 {
					m.previewScroll--
				}
			case "down", "ctrl+n", "j":
				m.previewScroll++

			// navigate between results without leaving preview
			case "left", "h", "[":
				if m.selected > 0 {
					m.selected--
					m.previewScroll = 0
				}
			case "right", "l", "]":
				if m.selected < len(m.results)-1 {
					m.selected++
					m.previewScroll = 0
				}

			// ④ open source file with system default app
			case "o":
				if err := openFile(m.results[m.selected].source); err == nil {
					m.notice = "Opened: " + m.results[m.selected].source
				} else {
					m.notice = "Open failed: " + err.Error()
				}
				return m, clearNotice(2 * time.Second)

			// ⑤ copy content to clipboard
			case "y":
				if err := copyToClipboard(m.results[m.selected].content); err == nil {
					m.notice = "✓ Copied to clipboard"
				} else {
					m.notice = "Copy failed: " + err.Error()
				}
				return m, clearNotice(2 * time.Second)
			}
			return m, nil
		}

		// ── search mode ───────────────────────────────────────────────────
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit

		case "q":
			if m.quitPending {
				return m, tea.Quit
			}
			// first press: show confirmation notice, clear after 2s
			m.quitPending = true
			m.notice = "Press q again to quit"
			return m, clearQuitConfirm(2 * time.Second)

		case "enter":
			if len(m.results) > 0 {
				m.mode = "preview"
				m.previewScroll = 0
			} else if strings.TrimSpace(m.input) != "" {
				m.loading = true
				m.results = nil
				if m.rerankEnabled && m.rerankMode == "llm" {
					m.rerankStreaming = true
					m.rerankThinking = ""
					m.rerankThinkExpand = false
					m.rerankBatchCurrent = 0
					m.rerankBatchTotal = 0
					return m, m.doRerankStream(m.input)
				}
				return m, m.doSearch(m.input)
			}
			return m, nil

		case "tab":
			if strings.TrimSpace(m.input) != "" {
				m.loading = true
				m.results = nil
				if m.rerankEnabled && m.rerankMode == "llm" {
					m.rerankStreaming = true
					m.rerankThinking = ""
					m.rerankThinkExpand = false
					m.rerankBatchCurrent = 0
					m.rerankBatchTotal = 0
					return m, m.doRerankStream(m.input)
				}
				return m, m.doSearch(m.input)
			}

		case "ctrl+o":
			if m.rerankStreaming || m.rerankThinking != "" {
				m.rerankThinkExpand = !m.rerankThinkExpand
			}
			return m, nil

		case "backspace":
			runes := []rune(m.input)
			if len(runes) > 0 {
				m.input = string(runes[:len(runes)-1])
				// ① debounce on backspace too
				m.loading = true
				m.results = nil
				return m, m.scheduleSearch()
			}

		case "up", "ctrl+p":
			if m.selected > 0 {
				m.selected--
			}

		case "down", "ctrl+n":
			if m.selected < len(m.results)-1 {
				m.selected++
			}

		case "esc":
			m.quitPending = false
			if m.input != "" {
				m.input = ""
				m.results = nil
				m.selected = 0
				m.loading = false
			} else {
				return m, tea.Quit
			}

		// ③ rerank picker
		case "r":
			m.mode = "rerank_picker"
			// pre-select current state
			switch {
			case !m.rerankEnabled:
				m.rerankPickerSelected = 0
			case m.rerankMode == "llm":
				m.rerankPickerSelected = 2
			default:
				m.rerankPickerSelected = 1
			}
			return m, nil

		// ⑥ open collection picker
		case "c":
			m.mode = "picker"
			// set cursor to current collection
			m.pickerSelected = 0
			for i, col := range m.collections {
				if col.Name == m.collection {
					m.pickerSelected = i + 1 // +1 for "all" entry
					break
				}
			}
			return m, nil

		case " ":
			m.quitPending = false
			m.input += " "
			m.loading = true
			m.results = nil
			return m, m.scheduleSearch()

		default:
			if msg.Type == tea.KeyRunes {
				m.quitPending = false // typing resets quit confirmation
				m.input += string(msg.Runes)
				// ① debounce: set loading flag, schedule timer
				m.loading = true
				m.results = nil
				return m, m.scheduleSearch()
			}
		}
	}
	return m, nil
}

// ── views ─────────────────────────────────────────────────────────────────────

func (m tuiModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	// ⑥ collection picker overlay
	if m.mode == "picker" {
		return m.pickerView()
	}

	// rerank picker overlay
	if m.mode == "rerank_picker" {
		return m.rerankPickerView()
	}

	if m.mode == "preview" && len(m.results) > 0 {
		return m.previewView()
	}

	var b strings.Builder

	// Title bar + rerank indicator + collection tag
	rerankLabel := styleRerankOff.Render("[rerank:off]")
	if m.rerankEnabled {
		switch m.rerankMode {
		case "llm":
			rerankLabel = styleRerankLLM.Render("[rerank:llm]")
		default:
			rerankLabel = styleRerankToken.Render("[rerank:token]")
		}
	}
	colLabel := ""
	if m.collection != "" {
		colLabel = "  " + styleCollectionTag.Render("📁 "+m.collection)
	}
	title := styleTitleBar.Render(" 🧠 axon — knowledge search ") + "  " + rerankLabel + colLabel
	b.WriteString(title + "\n\n")

	// Search input
	prompt := stylePrompt.Render("▶ ")
	inputLine := prompt + m.input
	if !m.loading {
		inputLine += styleDim.Render("█")
	}
	b.WriteString(inputLine + "\n")
	b.WriteString(styleDim.Render(strings.Repeat("─", minInt(m.width, 60))) + "\n\n")

	if m.notice != "" {
		b.WriteString(styleNotice.Render("  "+m.notice) + "\n\n")
	}

	if m.loading {
		if m.rerankStreaming {
			// LLM streaming rerank status line
			batchInfo := ""
			if m.rerankBatchTotal > 0 {
				batchInfo = fmt.Sprintf("  batch %d/%d", m.rerankBatchCurrent, m.rerankBatchTotal)
			}
			expandHint := styleThinkHint.Render("  ctrl+o to expand")
			if m.rerankThinkExpand {
				expandHint = styleThinkHint.Render("  ctrl+o to collapse")
			}
			b.WriteString(styleRerankLLM.Render("  🤔 Reranking with LLM...") + batchInfo + expandHint + "\n")

			// Thinking panel (expanded)
			if m.rerankThinkExpand && m.rerankThinking != "" {
				panelW := minInt(m.width-6, 72)
				if panelW < 20 {
					panelW = 40
				}
				// show last N lines of thinking to fit screen
				thinkLines := strings.Split(m.rerankThinking, "\n")
				maxThinkLines := m.height - 16
				if maxThinkLines < 3 {
					maxThinkLines = 3
				}
				if len(thinkLines) > maxThinkLines {
					thinkLines = thinkLines[len(thinkLines)-maxThinkLines:]
				}
				thinkContent := styleThinkText.Render(strings.Join(thinkLines, "\n"))
				box := styleThinkBorder.Width(panelW).Render(thinkContent)
				b.WriteString(box + "\n")
			}
		} else {
			b.WriteString(styleDim.Render("  Searching...") + "\n")
		}
	} else if m.err != nil {
		b.WriteString("  ❌ " + m.err.Error() + "\n")
	} else if len(m.results) == 0 && strings.TrimSpace(m.input) != "" {
		b.WriteString(styleDim.Render("  No results found.") + "\n")
	} else {
		maxDisplay := m.height - 10
		if maxDisplay < 1 {
			maxDisplay = 5
		}
		for i, r := range m.results {
			if i >= maxDisplay {
				b.WriteString(styleDim.Render(fmt.Sprintf("  ... %d more", len(m.results)-i)) + "\n")
				break
			}

			// CJK-safe snippet (80 runes)
			snippet := strings.ReplaceAll(r.content, "\n", " ")
			snippet = runesTruncate(snippet, 80, "...")

			scoreBadge := styleScore.Render(fmt.Sprintf("[%.3f]", r.score))
			// collection tag (only when searching across all collections)
			colTag := ""
			if m.collection == "" && r.collection != "" {
				colTag = " " + styleCollectionTag.Render("["+r.collection+"]")
			}
			line := fmt.Sprintf("  %s%s %s", scoreBadge, colTag, snippet)
			srcLine := fmt.Sprintf("       %s", styleSource.Render(r.source))

			if i == m.selected {
				b.WriteString(styleSelected.Render(line) + "\n")
				b.WriteString(styleSelected.Render(srcLine) + "\n")
			} else {
				b.WriteString(styleNormal.Render(line) + "\n")
				b.WriteString(styleDim.Render(srcLine) + "\n")
			}
		}
	}

	b.WriteString("\n")
	hintText := "  type to search  ↑↓ navigate  enter preview  r rerank  c collection  ctrl+o thinking  esc clear  q quit"
	if m.quitPending {
		hintText = "  " + styleNotice.Render("Press q again to quit") + "  (any other key to cancel)"
	}
	hint := styleHint.Render(hintText)
	b.WriteString(hint)

	return b.String()
}

func (m tuiModel) previewView() string {
	r := m.results[m.selected]

	var b strings.Builder

	// Title: show position + rerank status
	rerankTag := ""
	if m.rerankEnabled {
		label := "[rerank:token]"
		s := styleRerankToken
		if m.rerankMode == "llm" {
			label = "[rerank:llm]"
			s = styleRerankLLM
		}
		rerankTag = " " + s.Render(label)
	}
	title := styleTitleBar.Render(fmt.Sprintf(" Preview %d/%d ", m.selected+1, len(m.results))) + rerankTag
	b.WriteString(title + "\n\n")

	b.WriteString(styleSource.Render("  📄 "+r.source) + "\n")
	if r.collection != "" {
		b.WriteString(styleCollectionTag.Render("  📁 "+r.collection) + "\n")
	}
	b.WriteString(styleScore.Render(fmt.Sprintf("  score: %.4f", r.score)) + "\n\n")

	if m.notice != "" {
		b.WriteString(styleNotice.Render("  "+m.notice) + "\n\n")
	}

	// ② scrollable content
	maxW := m.width - 6
	if maxW < 20 {
		maxW = 40
	}
	boxH := m.height - 12
	if boxH < 5 {
		boxH = 5
	}

	allLines := wordWrapRunes(r.content, maxW)
	totalLines := len(allLines)

	// clamp scroll
	maxScroll := totalLines - boxH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.previewScroll > maxScroll {
		// can't modify m here (value receiver), but Update already clamps;
		// just use maxScroll for display
	}
	scroll := m.previewScroll
	if scroll > maxScroll {
		scroll = maxScroll
	}

	end := scroll + boxH
	if end > totalLines {
		end = totalLines
	}
	visibleLines := allLines[scroll:end]

	scrollInfo := ""
	if totalLines > boxH {
		scrollInfo = styleDim.Render(fmt.Sprintf(" (%d-%d / %d lines)", scroll+1, end, totalLines))
	}

	content := strings.Join(visibleLines, "\n")
	box := stylePreviewBox.Width(minInt(m.width-4, 80)).Render(content)
	b.WriteString(box + scrollInfo + "\n\n")

	hints := "  esc/q/enter back  ↑↓ scroll  [/] prev/next result  o open  y copy"
	b.WriteString(styleHint.Render(hints))
	return b.String()
}

// ── collection picker view ────────────────────────────────────────────────────

func (m tuiModel) pickerView() string {
	var b strings.Builder

	title := styleTitleBar.Render(" 📁 Switch Collection ")
	b.WriteString(title + "\n\n")

	// Build list: "All Collections" + each named collection
	type entry struct {
		label    string
		subLabel string
	}
	entries := make([]entry, 0, len(m.collections)+1)
	entries = append(entries, entry{label: "  All Collections", subLabel: "search across everything"})
	for _, col := range m.collections {
		sub := col.Type
		if col.Description != "" {
			sub = col.Description
		}
		entries = append(entries, entry{label: "  " + col.Name, subLabel: sub})
	}

	var rows []string
	for i, e := range entries {
		label := e.label
		if e.subLabel != "" {
			label += styleDim.Render("  — " + e.subLabel)
		}
		if i == m.pickerSelected {
			rows = append(rows, stylePickerSelected.Render(fmt.Sprintf("▶ %s", strings.TrimLeft(label, " "))))
		} else {
			rows = append(rows, stylePickerNormal.Render(fmt.Sprintf("  %s", strings.TrimLeft(label, " "))))
		}
	}

	if len(m.collections) == 0 {
		rows = append(rows, styleDim.Render("  (no collections found)"))
	}

	boxW := 50
	if m.width > 0 && m.width-8 < boxW {
		boxW = m.width - 8
	}
	box := stylePickerBorder.Width(boxW).Render(strings.Join(rows, "\n"))
	b.WriteString(box + "\n\n")

	hint := styleHint.Render("  ↑↓ navigate  enter select  esc cancel")
	b.WriteString(hint)
	return b.String()
}

// ── rerank picker view ────────────────────────────────────────────────────────

func (m tuiModel) rerankPickerView() string {
	var b strings.Builder

	title := styleTitleBar.Render(" ⚡ Rerank Mode ")
	b.WriteString(title + "\n\n")

	type entry struct {
		label string
		desc  string
	}
	entries := []entry{
		{label: "  Off", desc: "no reranking"},
		{label: "  Token", desc: "fast local BM25 rerank"},
		{label: "  LLM", desc: "slow but smarter, calls language model"},
	}

	var rows []string
	for i, e := range entries {
		label := e.label + styleDim.Render("  — "+e.desc)
		if i == m.rerankPickerSelected {
			rows = append(rows, stylePickerSelected.Render(fmt.Sprintf("▶ %s", strings.TrimLeft(label, " "))))
		} else {
			rows = append(rows, stylePickerNormal.Render(fmt.Sprintf("  %s", strings.TrimLeft(label, " "))))
		}
	}

	boxW := 52
	if m.width > 0 && m.width-8 < boxW {
		boxW = m.width - 8
	}
	box := stylePickerBorder.Width(boxW).Render(strings.Join(rows, "\n"))
	b.WriteString(box + "\n\n")

	hint := styleHint.Render("  ↑↓ navigate  enter select  esc cancel")
	b.WriteString(hint)
	return b.String()
}

// ── helpers ───────────────────────────────────────────────────────────────────

// runesTruncate truncates s to maxRunes runes, appending suffix if truncated.
// ④ fixes the CJK byte-slicing bug.
func runesTruncate(s string, maxRunes int, suffix string) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes-len([]rune(suffix))]) + suffix
}

// wordWrapRunes wraps text at maxWidth runes per line.
// ④ CJK-safe replacement for the old wordWrap.
func wordWrapRunes(text string, maxWidth int) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		runes := []rune(line)
		if len(runes) <= maxWidth {
			out = append(out, line)
			continue
		}
		for len(runes) > maxWidth {
			out = append(out, string(runes[:maxWidth]))
			runes = runes[maxWidth:]
		}
		if len(runes) > 0 {
			out = append(out, string(runes))
		}
	}
	return out
}

// clearNotice returns a command that clears the notice after d.
func clearNotice(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(_ time.Time) tea.Msg {
		return noticeMsg{}
	})
}

// clearQuitConfirm cancels the pending quit after d (user changed their mind).
func clearQuitConfirm(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(_ time.Time) tea.Msg {
		return quitCancelMsg{}
	})
}

// openFile opens path with the OS default application.
// ④ supports darwin, linux, windows.
func openFile(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

// copyToClipboard copies text to the system clipboard.
// ⑤ uses pbcopy (macOS) / xclip / xsel / clip.exe.
func copyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "windows":
		cmd = exec.Command("clip")
	default:
		// try xclip first, fall back to xsel
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		}
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── command ───────────────────────────────────────────────────────────────────

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Interactive search UI",
	Long:  "Launch an interactive terminal UI for searching your knowledge base.",
	RunE: func(cmd *cobra.Command, args []string) error {
		col, _ := cmd.Flags().GetString("collection")

		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}

		m := tuiModel{
			cfg:        cfg,
			collection: col,
			mode:       "search",
		}

		p := tea.NewProgram(m, tea.WithAltScreen())
		tuiProgram = p
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("tui: %w", err)
		}
		return nil
	},
}

func init() {
	tuiCmd.Flags().StringP("collection", "c", "", "Limit search to this collection (name or ID)")
}
