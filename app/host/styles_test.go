package main

import (
	"sort"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPasswordPromptMasksInputAndHandlesControlKeys(t *testing.T) {
	m := newPassModel("Password")
	updated, _ := m.Update(tea.KeyPressMsg{Code: 's', Text: "secret"})
	m = updated.(passModel)
	if m.ti.Value() != "secret" || strings.Contains(m.View().Content, "secret") {
		t.Fatal("password input must be stored and masked")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if completed := updated.(passModel); !completed.done || completed.View().Content != "" {
		t.Fatal("enter must finish and clear the password prompt")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cancelled := updated.(passModel); !cancelled.aborted || cancelled.View().Content != "" {
		t.Fatal("ctrl+c must cancel and clear the password prompt")
	}
}

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

	_, _ = m.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	if !m.selected[0] {
		t.Fatalf("expected row 0 selected after first toggle")
	}
	if len(m.selected) != 1 {
		t.Fatalf("expected 1 selected entry after first toggle, got %d", len(m.selected))
	}

	_, _ = m.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	if _, ok := m.selected[0]; ok {
		t.Fatalf("expected row 0 to be removed from selected map after deselect")
	}
	if len(m.selected) != 0 {
		t.Fatalf("expected empty selected map after deselect, got %d entries", len(m.selected))
	}
}
