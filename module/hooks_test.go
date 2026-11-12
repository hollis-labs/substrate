package module

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func hookLog(log *[]string, name string, err error) Hook {
	return func(_ context.Context, app *App) error {
		*log = append(*log, "hook:"+name+" started="+strings.Join(app.Started, "+"))
		return err
	}
}

func TestHooksRunBetweenStartsInOrder(t *testing.T) {
	var log []string
	withRegistry(t, probe{name: "config", log: &log}, probe{name: "db", log: &log}, probe{name: "web", log: &log})
	var rt Runtime
	h := Hooks{
		AfterConfig: hookLog(&log, "after", nil),
		Before:      map[string]Hook{"config": hookLog(&log, "b-config", nil), "db": hookLog(&log, "b-db", nil)},
	}
	if err := rt.StartWith(context.Background(), newApp(), []string{"config", "db", "web"}, h); err != nil {
		t.Fatal(err)
	}
	want := "hook:b-config started=,start:config,hook:after started=config,hook:b-db started=config,start:db,start:web"
	if got := strings.Join(log, ","); got != want {
		t.Fatalf("order\n got %s\nwant %s", got, want)
	}
}

func TestHookNamingAModuleNotInTheStartListIsAnErrorBeforeAnythingStarts(t *testing.T) {
	var log []string
	withRegistry(t, probe{name: "config", log: &log}, probe{name: "db", log: &log})
	var rt Runtime
	err := rt.StartWith(context.Background(), newApp(), []string{"config"}, Hooks{Before: map[string]Hook{"dbb": hookLog(&log, "x", nil)}})
	if err == nil || !strings.Contains(err.Error(), `"dbb"`) {
		t.Fatalf("want an error naming dbb, got %v", err)
	}
	// A linked module that is not selected is as wrong as a typo.
	err = rt.StartWith(context.Background(), newApp(), []string{"config"}, Hooks{Before: map[string]Hook{"db": hookLog(&log, "x", nil)}})
	if err == nil {
		t.Fatal("hook for a linked but unselected module must be an error")
	}
	if len(log) != 0 {
		t.Fatalf("nothing may start or run: %v", log)
	}
}

func TestHookErrorStopsStartedModulesInReverse(t *testing.T) {
	for _, tc := range []struct {
		name  string
		names []string
		h     func(log *[]string) Hooks
		want  string
	}{
		{"before", []string{"a", "b", "c"}, func(l *[]string) Hooks {
			return Hooks{Before: map[string]Hook{"c": hookLog(l, "c", errors.New("nope"))}}
		}, "start:a,start:b,hook:c started=a+b,stop:b,stop:a"},
		{"after-config", []string{"config", "a"}, func(l *[]string) Hooks {
			return Hooks{AfterConfig: hookLog(l, "after", errors.New("nope"))}
		}, "start:config,hook:after started=config,stop:config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var log []string
			withRegistry(t, probe{name: "config", log: &log}, probe{name: "a", log: &log}, probe{name: "b", log: &log}, probe{name: "c", log: &log})
			var rt Runtime
			err := rt.StartWith(context.Background(), newApp(), tc.names, tc.h(&log))
			if err == nil || !strings.Contains(err.Error(), "nope") {
				t.Fatalf("want hook error, got %v", err)
			}
			if got := strings.Join(log, ","); got != tc.want {
				t.Fatalf("log\n got %s\nwant %s", got, tc.want)
			}
			// Already stopped: a later Stop must not stop anything twice.
			n := len(log)
			_ = rt.Stop(context.Background())
			if len(log) != n {
				t.Fatalf("stopped twice: %v", log[n:])
			}
		})
	}
}

func TestAfterConfigWithoutConfigModuleRunsOnceBeforeTheFirstModule(t *testing.T) {
	var log []string
	withRegistry(t, probe{name: "a", log: &log}, probe{name: "b", log: &log})
	var rt Runtime
	if err := rt.StartWith(context.Background(), newApp(), []string{"a", "b"}, Hooks{AfterConfig: hookLog(&log, "after", nil)}); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(log, ","), "hook:after started=,start:a,start:b"; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestHooksRunOncePerStartAndAgainOnRestart(t *testing.T) {
	var log []string
	withRegistry(t, probe{name: "config", log: &log}, probe{name: "a", log: &log})
	h := Hooks{AfterConfig: hookLog(&log, "after", nil), Before: map[string]Hook{"a": hookLog(&log, "b-a", nil)}}
	for cycle := 1; cycle <= 2; cycle++ {
		log = nil
		var rt Runtime
		if err := rt.StartWith(context.Background(), newApp(), []string{"config", "a"}, h); err != nil {
			t.Fatal(err)
		}
		if err := rt.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, l := range log {
			if strings.HasPrefix(l, "hook:") {
				n++
			}
		}
		if n != 2 {
			t.Fatalf("cycle %d: want each hook once, got %v", cycle, log)
		}
	}
}

func TestNoHooksIsPlainStart(t *testing.T) {
	var log []string
	withRegistry(t, probe{name: "a", log: &log})
	var rt Runtime
	if err := rt.StartWith(context.Background(), newApp(), []string{"a"}, Hooks{}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(log, ",") != "start:a" {
		t.Fatal(log)
	}
}
