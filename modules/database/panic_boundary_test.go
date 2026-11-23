package database

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

type hostilePanicValue struct{}

func (hostilePanicValue) String() string { panic("nested-private-panic") }

func TestHookPanicOnlyLogsReferenceAndRemainsUsable(t *testing.T) {
	for _, value := range []any{"private-panic-marker", hostilePanicValue{}} {
		var out bytes.Buffer
		log := slog.New(slog.NewTextHandler(&out, nil))
		callHook(func(Status) { panic(value) }, Status{}, log)
		got := out.String()
		if !strings.Contains(got, "ref=pnc_") || strings.Contains(got, "private-panic") {
			t.Fatalf("panic must leave only a reference: %s", got)
		}
		called := false
		callHook(func(Status) { called = true }, Status{}, log)
		if !called {
			t.Fatal("subsequent callback did not run")
		}
	}
}
