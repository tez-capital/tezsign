package main

import (
	"sort"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSelectedRowIDsIgnoresFalseAndOutOfRange(t *testing.T) {
	rows := []statusRow{
		{ID: "key-a"},
		{ID: "key-b"},
	}
	selected := map[int]bool{
		0:  true,
		1:  false,
		2:  true,
		-1: true,
	}

	got := selectedRowIDs(rows, selected)
	sort.Strings(got)

	want := []string{"key-a"}
	if len(got) != len(want) {
		t.Fatalf("unexpected selected count: got=%d want=%d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected selected ids: got=%v want=%v", got, want)
		}
	}
}

func TestKeyPickerToggleSpaceDeletesDeselectedRow(t *testing.T) {
	m := newKeyPickerFromRows([]statusRow{{ID: "key-a"}}, 80)

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if !m.selected[0] {
		t.Fatalf("expected row 0 selected after first toggle")
	}
	if len(m.selected) != 1 {
		t.Fatalf("expected 1 selected entry after first toggle, got %d", len(m.selected))
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if _, ok := m.selected[0]; ok {
		t.Fatalf("expected row 0 to be removed from selected map after deselect")
	}
	if len(m.selected) != 0 {
		t.Fatalf("expected empty selected map after deselect, got %d entries", len(m.selected))
	}
}
