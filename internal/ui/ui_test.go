package ui

import (
	"errors"
	"strings"
	"testing"
)

func TestOutput(t *testing.T) {
	u := NewTest("")
	u.Info("a %d", 1)
	u.Row("Postgres", "x")
	if got := u.Output(); got != "▸ a 1\n  Postgres    x\n" {
		t.Fatalf("%q", got)
	}
	u.Color = true
	if u.Good("ok") != "\x1b[32mok\x1b[0m" || Pad(u.Good("ok"), 4) != "\x1b[32mok\x1b[0m  " {
		t.Fatal("colour")
	}
}

func TestLinePromptsShareReader(t *testing.T) {
	u := NewTest("2\nshop\ny\n\n")
	i, err := u.Choose("Kind", []string{"a", "b"}, 0)
	if err != nil || i != 1 {
		t.Fatalf("%d %v", i, err)
	}
	if s, _ := u.Ask("Name", ""); s != "shop" {
		t.Fatal(s)
	}
	if ok, _ := u.Confirm("Ok?", false); !ok {
		t.Fatal("confirm")
	}
	if s, _ := u.Ask("Owner", "shop"); s != "shop" || !strings.Contains(u.Output(), "Owner [shop]: ") {
		t.Fatal("default")
	}
}

func TestEscAndEOF(t *testing.T) {
	if _, err := NewTest("\x1b\n").Ask("Name", ""); !errors.Is(err, ErrCancelled) {
		t.Fatal(err)
	}
	if _, err := NewTest("").Ask("Name", "x"); !errors.Is(err, ErrNoInput) {
		t.Fatal(err)
	}
	if s, err := NewTest("last").Ask("Name", ""); s != "last" || err != nil {
		t.Fatal("unterminated last line is accepted")
	}
	if c, err := NewTest("\x1b\n").Menu(nil); c != "b" || err != nil {
		t.Fatal("ESC in a menu means back")
	}
}

func TestPickByNumberOrName(t *testing.T) {
	u := NewTest("9\nshop\n2\n")
	if s, _ := u.Pick("DB", []string{"app", "shop"}); s != "shop" {
		t.Fatal(s)
	}
	if s, _ := u.Pick("DB", []string{"app", "shop"}); s != "shop" {
		t.Fatal(s)
	}
}
