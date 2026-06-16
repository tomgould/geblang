package evaluator

import (
	"bufio"
	"strings"
	"testing"
)

func TestMultiChooseStateNavigationAndToggle(t *testing.T) {
	s := &multiChooseState{checked: make([]bool, 3)}
	s.apply(keyDown)
	s.apply(keyDown)
	if s.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", s.cursor)
	}
	s.apply(keyDown)
	if s.cursor != 0 {
		t.Fatalf("cursor wrap = %d, want 0", s.cursor)
	}
	s.apply(keyUp)
	if s.cursor != 2 {
		t.Fatalf("cursor up-wrap = %d, want 2", s.cursor)
	}
	s.apply(keyToggle)
	if !s.checked[2] {
		t.Fatal("index 2 should be checked after toggle")
	}
	s.apply(keyToggleAll)
	for i, c := range s.checked {
		if !c {
			t.Fatalf("toggleAll: index %d not checked", i)
		}
	}
	s.apply(keyToggleAll)
	for i, c := range s.checked {
		if c {
			t.Fatalf("toggleAll off: index %d still checked", i)
		}
	}
}

func TestMultiChooseConfirmCancelAndSelection(t *testing.T) {
	s := &multiChooseState{checked: []bool{true, false, true}}
	s.apply(keyConfirm)
	if !s.done || s.cancel {
		t.Fatal("confirm should set done, not cancel")
	}
	sel := selectedChoices([]string{"a", "b", "c"}, s.checked)
	if len(sel) != 2 || sel[0] != "a" || sel[1] != "c" {
		t.Fatalf("selected = %v, want [a c]", sel)
	}

	c := &multiChooseState{checked: make([]bool, 2)}
	c.apply(keyCancel)
	if !c.cancel {
		t.Fatal("cancel should set cancel")
	}
}

func TestReadTUIKeyDecoding(t *testing.T) {
	cases := []struct {
		in   string
		want tuiKey
	}{
		{" ", keyToggle},
		{"\r", keyConfirm},
		{"\n", keyConfirm},
		{"a", keyToggleAll},
		{"j", keyDown},
		{"k", keyUp},
		{"q", keyCancel},
		{"\x03", keyCancel},
		{"\x1b[A", keyUp},
		{"\x1b[B", keyDown},
	}
	for _, c := range cases {
		got, err := readTUIKey(bufio.NewReader(strings.NewReader(c.in)))
		if err != nil {
			t.Fatalf("readTUIKey(%q) err: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("readTUIKey(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
