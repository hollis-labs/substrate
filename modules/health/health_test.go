package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newRegistry() *Registry { return &Registry{checks: map[string]Check{}, soft: map[string]bool{}} }

func serve(r *Registry) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	return rec
}

func TestSoftFailureIsDegradedButStill200(t *testing.T) {
	r := newRegistry()
	r.Add("db", func(context.Context) error { return nil })
	r.AddSoft("database.data", func(context.Context) error { return errors.New("down") })
	rep := r.Report(context.Background())
	if rep.Status != "degraded" || rep.Checks["database.data"] != "down" || rep.Checks["db"] != "ok" {
		t.Fatalf("report = %+v", rep)
	}
	if rec := serve(r); rec.Code != http.StatusOK {
		t.Fatalf("a soft failure must not fail the probe: %d %s", rec.Code, rec.Body)
	}
}

func TestHardFailureIs503EvenWithSoftChecks(t *testing.T) {
	r := newRegistry()
	r.AddSoft("database.data", func(context.Context) error { return nil })
	r.Add("db", func(context.Context) error { return errors.New("down") })
	if rec := serve(r); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("hard failure = %d", rec.Code)
	}
}

func TestAddAfterAddSoftMakesTheCheckHard(t *testing.T) {
	r := newRegistry()
	fail := func(context.Context) error { return errors.New("down") }
	r.AddSoft("x", fail)
	r.Add("x", fail)
	if rec := serve(r); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("re-registered as hard = %d", rec.Code)
	}
}

func TestAllOKIs200Ok(t *testing.T) {
	r := newRegistry()
	r.Add("a", func(context.Context) error { return nil })
	rec := serve(r)
	if rec.Code != 200 || r.Report(context.Background()).Status != "ok" {
		t.Fatalf("ok report = %d", rec.Code)
	}
}
