package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type selectionStage int

const (
	stageDevice selectionStage = iota
)

type deviceCandidate struct {
	Name      string
	Path      string
	SizeBytes uint64
	Model     string
	Status    string
	Valid     bool
}

type selectionModel struct {
	stage          selectionStage
	devices        []deviceCandidate
	table          table.Model
	selectedDevice *deviceCandidate
	err            error
}

func newSelectionModel(devices []deviceCandidate) selectionModel {
	columns := []table.Column{
		{Title: "Name", Width: 10},
		{Title: "Size", Width: 12},
		{Title: "Status", Width: 66},
		{Title: "Model", Width: 18},
		{Title: "Path", Width: 20},
	}

	rows := make([]table.Row, 0, len(devices))
	for _, dev := range devices {
		rows = append(rows, table.Row{
			dev.Name,
			byteCountToHumanReadable(int64(dev.SizeBytes)),
			dev.Status,
			dev.Model,
			dev.Path,
		})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(10),
	)
	t.SetStyles(newTableStyles())

	return selectionModel{
		stage:   stageDevice,
		devices: devices,
		table:   t,
	}
}

func newTableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(true)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	return s
}

func (m selectionModel) Init() tea.Cmd {
	return nil
}

func (m selectionModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}

	switch m.stage {
	case stageDevice:
		if len(m.devices) == 0 {
			return "No TezSign SD cards detected. Press q to exit."
		}
		return fmt.Sprintf(
			"Select a device (↑/↓ to navigate, enter to start full update)\n\n%s\n\nPress enter to continue or q to cancel.",
			m.table.View(),
		)
	default:
		return ""
	}
}

func (m selectionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.err = errors.New("update cancelled")
			return m, tea.Quit
		}

		switch m.stage {
		case stageDevice:
			switch msg.String() {
			case "up", "k":
				m.table.MoveUp(1)
			case "down", "j":
				m.table.MoveDown(1)
			case "enter":
				if len(m.devices) == 0 {
					m.err = errors.New("no TezSign SD cards detected")
					return m, tea.Quit
				}
				cursor := m.table.Cursor()
				if cursor < 0 || cursor >= len(m.devices) {
					m.err = errors.New("invalid selection")
					return m, tea.Quit
				}
				if !m.devices[cursor].Valid {
					m.err = errors.New("selected device is not a valid TezSign SD card")
					return m, tea.Quit
				}
				m.selectedDevice = &m.devices[cursor]
				return m, tea.Quit
			}
		}
	case tea.WindowSizeMsg:
		width := msg.Width - 4
		if width < 20 {
			width = 20
		}
		m.table.SetWidth(width)
	}

	return m, nil
}

type progressCounter interface {
	Count() int64
}

type progressModel struct {
	title   string
	total   int64
	counter progressCounter
	cancel  func()
	err     error
	done    bool
}

type tickMsg time.Time

type finishMsg struct {
	err error
}

func newProgressModel(title string, total int64, counter progressCounter, cancel func()) progressModel {
	return progressModel{
		title:   title,
		total:   total,
		counter: counter,
		cancel:  cancel,
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m progressModel) Init() tea.Cmd {
	return tickCmd()
}

func (m progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		if m.done {
			return m, nil
		}
		return m, tickCmd()
	case finishMsg:
		m.done = true
		m.err = msg.err
		return m, tea.Quit
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			if !m.done && m.cancel != nil {
				m.cancel()
			}
			m.done = true
			m.err = errors.New("operation cancelled")
			return m, tea.Quit
		}
	}
	return m, nil
}

func renderProgressBar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	fill := int((pct / 100) * float64(width))
	if fill > width {
		fill = width
	}
	return fmt.Sprintf("[%s%s] %5.1f%%", strings.Repeat("█", fill), strings.Repeat("░", width-fill), pct)
}

func (m progressModel) View() string {
	read := m.counter.Count()
	var pct float64 = -1
	if m.total > 0 {
		pct = float64(read) / float64(m.total) * 100
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("%s\n\n", m.title))
	if pct >= 0 {
		builder.WriteString(renderProgressBar(pct, 40))
		builder.WriteString(fmt.Sprintf("  %s / %s\n", byteCountToHumanReadable(read), byteCountToHumanReadable(m.total)))
	} else {
		builder.WriteString(fmt.Sprintf("%s read\n", byteCountToHumanReadable(read)))
	}

	if m.done {
		if m.err != nil {
			builder.WriteString(fmt.Sprintf("\nError: %v\n", m.err))
		} else {
			builder.WriteString("\nDone.\n")
		}
	} else {
		builder.WriteString("\nPress q to cancel.")
	}
	return builder.String()
}

type countingReader struct {
	r    io.Reader
	read int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	atomic.AddInt64(&c.read, int64(n))
	return n, err
}

func (c *countingReader) BytesRead() int64 {
	return atomic.LoadInt64(&c.read)
}

func (c *countingReader) Count() int64 {
	return c.BytesRead()
}

type countingWriter struct {
	w       io.Writer
	written int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	atomic.AddInt64(&c.written, int64(n))
	return n, err
}

func (c *countingWriter) BytesWritten() int64 {
	return atomic.LoadInt64(&c.written)
}

func (c *countingWriter) Count() int64 {
	return c.BytesWritten()
}

// Helper to convert byte counts to human-readable strings (e.g., MB, GB)
func byteCountToHumanReadable(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
