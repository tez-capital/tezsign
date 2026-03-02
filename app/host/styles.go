package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/x/term"
	"github.com/tez-capital/tezsign/broker"
	"github.com/tez-capital/tezsign/common"
	"github.com/tez-capital/tezsign/signerpb"
)

type keyStateJSON struct {
	ID  string `json:"id"`
	OK  bool   `json:"ok"`
	Err string `json:"error,omitempty"`
}

type keyStatusJSON struct {
	ID                   string `json:"id"`
	LockState            string `json:"lock_state"`
	TZ4                  string `json:"tz4"`
	BLPubkey             string `json:"bl_pubkey"`
	Pop                  string `json:"pop"`
	LastBlockLevel       uint64 `json:"last_block_level"`
	LastBlockRound       uint32 `json:"last_block_round"`
	LastPreattestLevel   uint64 `json:"last_preattestation_level"`
	LastPreattestRound   uint32 `json:"last_preattestation_round"`
	LastAttestationLevel uint64 `json:"last_attestation_level"`
	LastAttestationRound uint32 `json:"last_attestation_round"`
	StateCorrupted       bool   `json:"state_corrupted"`
}

func getKeysStatusJSON(ks *signerpb.KeyStatus) keyStatusJSON {
	return keyStatusJSON{
		ID:                   ks.GetKeyId(),
		LockState:            ks.GetLockState().String(),
		TZ4:                  ks.GetTz4(),
		BLPubkey:             ks.GetBlPubkey(),
		Pop:                  ks.GetPop(),
		LastBlockLevel:       ks.GetLastBlockLevel(),
		LastBlockRound:       ks.GetLastBlockRound(),
		LastPreattestLevel:   ks.GetLastPreattestationLevel(),
		LastPreattestRound:   ks.GetLastPreattestationRound(),
		LastAttestationLevel: ks.GetLastAttestationLevel(),
		LastAttestationRound: ks.GetLastAttestationRound(),
		StateCorrupted:       ks.GetStateCorrupted(),
	}
}

var (
	// adaptive colors look good in light/dark terminals
	borderColor = lipgloss.AdaptiveColor{Light: "#6C6CFF", Dark: "#6C6CFF"}
	chipColor   = lipgloss.AdaptiveColor{Light: "#000000", Dark: "#FFFFFF"}
	okColor     = lipgloss.AdaptiveColor{Light: "#006400", Dark: "#9FF29A"}
	errColor    = lipgloss.AdaptiveColor{Light: "#8B0000", Dark: "#FF6B6B"}

	baseCell     = lipgloss.NewStyle().Padding(0, 1)
	chipStyle    = baseCell.MarginRight(1).Border(lipgloss.RoundedBorder()).BorderForeground(borderColor).Bold(true).Foreground(chipColor)
	chipOkStyle  = chipStyle.Foreground(okColor)
	chipErrStyle = chipStyle.Foreground(errColor)

	headerStyle    = lipgloss.NewStyle().Bold(true)
	stateUnlocked  = lipgloss.NewStyle().Foreground(okColor).Bold(true)
	stateLocked    = lipgloss.NewStyle().Foreground(errColor).Bold(true)
	stateCorrupted = lipgloss.NewStyle().Foreground(errColor).Bold(true)
)

// --- Bubble Tea password prompt ---
type passModel struct {
	ti      textinput.Model
	prompt  string
	done    bool
	aborted bool
}

func newPassModel(prompt string) passModel {
	ti := textinput.New()
	ti.Placeholder = ""
	ti.Prompt = prompt + ": "
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•' // or '*'
	ti.Focus()
	return passModel{ti: ti, prompt: prompt}
}

func (m passModel) Init() tea.Cmd { return textinput.Blink }

func (m passModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			m.done = true
			return m, tea.Quit
		case tea.KeyCtrlC, tea.KeyEsc:
			m.aborted = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.ti, cmd = m.ti.Update(msg)
	return m, cmd
}

func (m passModel) View() string {
	if m.done || m.aborted {
		return ""
	}
	return "\n" + m.ti.View() + "\n"
}

func isTTY(f *os.File) bool {
	return term.IsTerminal(f.Fd())
}

// obtainPassword prompts for a password using Bubble Tea when interactive.
// Order of precedence:
//  1. TEZSIGN_UNLOCK_PASS env
//  2. Bubble Tea masked prompt if stdout is a TTY
//
// Returns a zero-copy []byte the caller must wipe via secure.MemoryWipe.
func obtainPassword(prompt string, withEnv bool) ([]byte, error) {
	// 1) env

	if v := strings.TrimSpace(os.Getenv(envPass)); withEnv && v != "" {
		return []byte(v), nil
	}

	// 2) interactive? (stdout TTY)
	interactive := isTTY(os.Stdout) && isTTY(os.Stdin)

	if interactive {
		m := newPassModel(prompt)
		prog := tea.NewProgram(m)
		res, err := prog.Run()
		if err != nil {
			// fall through to non-TTY fallback
		} else {
			pm := res.(passModel)
			if pm.aborted {
				return nil, ErrAborted
			}
			val := strings.TrimSpace(pm.ti.Value())
			if val == "" {
				return nil, ErrEmptyPassphrase
			}

			return []byte(val), nil
		}
	}

	return nil, ErrEmptyPassphrase
}

// renderAliasChips lays out multi-line “chips” with the provided style and wraps by terminal width.
func renderChips(labels []string, style lipgloss.Style, maxWidth int) string {
	if maxWidth < 30 {
		maxWidth = 30
	}
	// pre-render
	chips := make([]string, len(labels))
	for i, s := range labels {
		chips[i] = style.Render(s)
	}

	var lines []string
	var row []string
	rowW := 0

	flush := func() {
		if len(row) == 0 {
			return
		}
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, row...))
		row = row[:0]
		rowW = 0
	}

	for _, chip := range chips {
		w := lipgloss.Width(chip)
		if rowW > 0 && rowW+w > maxWidth {
			flush()
		}
		row = append(row, chip)
		rowW += w
	}

	flush()
	return strings.Join(lines, "\n")
}

func renderAliasChips(ids []string, maxWidth int) string {
	return renderChips(ids, chipStyle, maxWidth)
}

// ---------- pretty TTY status table ----------

type statusRow struct {
	ID, State, TZ4 string
	BLevel         uint64
	BRound         uint32
	PLevel         uint64
	PRound         uint32
	ALevel         uint64
	ARound         uint32
}

func chipState(s string) string {
	switch s {
	case "UNLOCKED":
		return stateUnlocked.Render("UNLOCKED")
	case "LOCKED":
		return stateLocked.Render("LOCKED")
	case "CORRUPTED":
		return stateCorrupted.Render("CORRUPTED")
	default:
		return s
	}
}

type statusTableOpts struct {
	Selectable bool         // add first "select" column with cursor + checkbox
	Selected   map[int]bool // which data-row indexes are selected
	Cursor     int          // which data-row is focused; -1 to disable
}

func renderStatusTable(rows []statusRow, opts statusTableOpts) string {
	// Build data rows
	data := make([][]string, 0, len(rows))
	for i, r := range rows {
		b := fmt.Sprintf("%d • %d", r.BLevel, r.BRound)
		p := fmt.Sprintf("%d • %d", r.PLevel, r.PRound)
		a := fmt.Sprintf("%d • %d", r.ALevel, r.ARound)

		row := []string{}
		if opts.Selectable {
			cur := " "
			if i == opts.Cursor {
				cur = "›"
			}
			box := "□"
			if opts.Selected != nil && opts.Selected[i] {
				box = "■"
			}
			row = append(row, cur+" "+box) // "select" column
		}

		row = append(row,
			r.ID,
			chipState(r.State),
			r.TZ4,
			b, p, a,
		)
		data = append(data, row)
	}

	// Headers
	headers := []string{}
	if opts.Selectable {
		headers = append(headers, headerStyle.Render("select"))
	}
	headers = append(headers,
		headerStyle.Render("id"),
		headerStyle.Render("state"),
		headerStyle.Render("tz4"),
		headerStyle.Render("Block"),
		headerStyle.Render("PreAtt"),
		headerStyle.Render("Att"),
	)

	// Table
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(borderColor)).
		Headers(headers...).
		Rows(data...).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := baseCell

			// Right align numeric columns: at the end, always last 3 columns
			// When Selectable: columns are [0:select, 1:id, 2:state, 3:tz4, 4:Block, 5:PreAtt, 6:Att]
			// When not:        columns are [0:id,     1:state, 2:tz4, 3:Block, 4:PreAtt, 5:Att]
			last3Start := 4
			if !opts.Selectable {
				last3Start = 3
			}
			if col >= last3Start {
				s = s.Align(lipgloss.Right)
			} else {
				s = s.Align(lipgloss.Left)
			}

			// Highlight the cursor row (only in selectable mode)
			if opts.Selectable && row == opts.Cursor {
				s = s.Bold(true)
			}
			return s
		})

	return t.Render()
}

// ---------- pretty TTY lock/unlock table ----------

type keyPickerModel struct {
	rows     []statusRow
	cursor   int
	selected map[int]bool
	width    int
	aborted  bool
}

func newKeyPickerFromRows(rows []statusRow, width int) *keyPickerModel {
	return &keyPickerModel{
		rows:     rows,
		cursor:   0,
		selected: make(map[int]bool),
		width:    width,
	}
}

func (m *keyPickerModel) Init() tea.Cmd { return nil }

func (m *keyPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.aborted = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.rows)-1 {
				m.cursor++
			}
		case " ":
			if len(m.rows) > 0 {
				if m.selected[m.cursor] {
					// Keep map sparse: selected keys exist, deselected keys are removed.
					delete(m.selected, m.cursor)
				} else {
					m.selected[m.cursor] = true
				}
			}
		case "a": // toggle all
			all := allRowsSelected(m.rows, m.selected)
			m.selected = make(map[int]bool)
			if !all {
				for i := range m.rows {
					m.selected[i] = true
				}
			}
		case "enter":
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
	}
	return m, nil
}

func (m *keyPickerModel) View() string {
	if len(m.rows) == 0 {
		return "No keys found.\n\nPress Esc to exit."
	}
	body := renderStatusTable(m.rows, statusTableOpts{
		Selectable: true,
		Selected:   m.selected,
		Cursor:     m.cursor,
	})
	help := "\n↑/↓ or j/k move • space toggle • a all/none • enter confirm • esc cancel\n"
	border := lipgloss.NewStyle().BorderForeground(borderColor)
	return border.Render(body) + help
}

func runKeyPicker(b *broker.Broker) (selectedIDs []string, aborted bool, err error) {
	st, err := common.ReqStatus(b)
	if err != nil {
		return nil, false, err
	}
	rows := statusRows(st.GetKeys())

	m := newKeyPickerFromRows(rows, 80)
	res, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return nil, false, err
	}
	pm := res.(*keyPickerModel)
	if pm.aborted {
		return nil, true, nil
	}
	selectedIDs = selectedRowIDs(pm.rows, pm.selected)
	sort.Strings(selectedIDs)
	return selectedIDs, false, nil
}

func allRowsSelected(rows []statusRow, selected map[int]bool) bool {
	if len(rows) == 0 {
		return false
	}
	for i := range rows {
		if !selected[i] {
			return false
		}
	}
	return true
}

func selectedRowIDs(rows []statusRow, selected map[int]bool) []string {
	out := make([]string, 0, len(selected))
	for idx, isSelected := range selected {
		if !isSelected {
			continue
		}
		if idx < 0 || idx >= len(rows) {
			continue
		}
		out = append(out, rows[idx].ID)
	}
	return out
}

func statusRows(statuses []*signerpb.KeyStatus) []statusRow {
	rows := make([]statusRow, 0, len(statuses))
	for _, ks := range statuses {
		state := ks.GetLockState().String()

		if ks.GetStateCorrupted() {
			state = "CORRUPTED"
		}

		row := statusRow{
			ID:     ks.GetKeyId(),
			State:  state,
			TZ4:    ks.GetTz4(),
			BLevel: ks.GetLastBlockLevel(), BRound: ks.GetLastBlockRound(),
			PLevel: ks.GetLastPreattestationLevel(), PRound: ks.GetLastPreattestationRound(),
			ALevel: ks.GetLastAttestationLevel(), ARound: ks.GetLastAttestationRound(),
		}

		if ks.GetStateCorrupted() {
			row.BLevel, row.BRound = 0, 0
			row.PLevel, row.PRound = 0, 0
			row.ALevel, row.ARound = 0, 0
		}

		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}
