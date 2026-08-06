package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A caller-supplied correlation id is untrusted input that ends up in log
// lines, so anything that could forge a line or smuggle control characters must
// be rejected and replaced by a generated one.
func TestSanitizeCorrelationID(t *testing.T) {
	accepted := []string{"abc123", "req-1a2b3c", "trace_id_42", strings.Repeat("a", 64)}
	for _, value := range accepted {
		if sanitizeCorrelationID(value) != value {
			t.Errorf("%q should be accepted", value)
		}
	}

	rejected := []string{
		"",
		strings.Repeat("a", 65), // too long
		"line\ninjected",        // newline forges a log line
		"tab\there",             // control character
		`{"level":"error"}`,     // structured-log forgery
		"has space",
		"semi;colon",
	}
	for _, value := range rejected {
		if got := sanitizeCorrelationID(value); got != "" {
			t.Errorf("%q should be rejected, got %q", value, got)
		}
	}
}

func TestCorrelationIDIsAssignedAndEchoed(t *testing.T) {
	app, err := NewWithOptions(Options{})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Routes()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	generated := rec.Header().Get(correlationHeader)
	if generated == "" {
		t.Fatal("every response must carry a correlation id")
	}
	if !strings.HasPrefix(generated, "req-") {
		t.Fatalf("unexpected generated id %q", generated)
	}
}

// A valid client id is adopted so a caller can join its own trace to ours.
func TestCorrelationIDFromClientIsAdopted(t *testing.T) {
	app, err := NewWithOptions(Options{})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Routes()

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set(correlationHeader, "client-trace-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get(correlationHeader); got != "client-trace-1" {
		t.Fatalf("client correlation id should be adopted, got %q", got)
	}
}

// A hostile id must never be reflected back or logged; a fresh one takes over.
func TestHostileCorrelationIDIsReplaced(t *testing.T) {
	app, err := NewWithOptions(Options{})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Routes()

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set(correlationHeader, "evil\ninjected line")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	got := rec.Header().Get(correlationHeader)
	if strings.Contains(got, "\n") || strings.Contains(got, "evil") {
		t.Fatalf("a hostile correlation id was reflected: %q", got)
	}
	if !strings.HasPrefix(got, "req-") {
		t.Fatalf("expected a generated id, got %q", got)
	}
}

func TestLogLevelForStatus(t *testing.T) {
	cases := map[int]string{200: "info", 202: "info", 400: "warn", 401: "warn", 500: "error", 503: "error"}
	for status, want := range cases {
		if got := logLevelForStatus(status); got != want {
			t.Errorf("status %d -> %q, want %q", status, got, want)
		}
	}
}
