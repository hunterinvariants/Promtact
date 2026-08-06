package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseTraceparent(t *testing.T) {
	traceID, parentID, sampled, ok := parseTraceparent("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	if !ok || traceID != "4bf92f3577b34da6a3ce929d0e0e4736" || parentID != "00f067aa0ba902b7" || !sampled {
		t.Fatalf("valid header not parsed: %q %q %v %v", traceID, parentID, sampled, ok)
	}

	// Malformed or spec-invalid headers must be rejected rather than producing a
	// broken trace that silently loses spans downstream.
	for _, bad := range []string{
		"",
		"garbage",
		"01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", // unsupported version
		"00-tooshort-00f067aa0ba902b7-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-short-01",
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01", // all-zero trace id
		"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01", // all-zero span id
		"00-zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz-00f067aa0ba902b7-01", // not hex
	} {
		if _, _, _, ok := parseTraceparent(bad); ok {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

// A caller's trace must continue through the gateway, otherwise the agent's own
// trace and the enforcement decision cannot be joined afterwards.
func TestIncomingTraceIsContinued(t *testing.T) {
	incoming := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	span := newSpanContext(incoming)
	if span.traceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace id was not continued: %q", span.traceID)
	}
	if span.parentID != "00f067aa0ba902b7" {
		t.Fatalf("parent span was not recorded: %q", span.parentID)
	}
	if span.spanID == span.parentID {
		t.Fatal("the server span must be its own span, not the caller's")
	}

	fresh := newSpanContext("")
	if len(fresh.traceID) != 32 || len(fresh.spanID) != 16 {
		t.Fatalf("generated ids have the wrong shape: %+v", fresh)
	}
	if fresh.parentID != "" {
		t.Fatal("a fresh trace has no parent")
	}
}

func TestTraceparentRoundTrip(t *testing.T) {
	span := newSpanContext("")
	traceID, parentID, sampled, ok := parseTraceparent(span.traceparent())
	if !ok || traceID != span.traceID || parentID != span.spanID || !sampled {
		t.Fatalf("emitted traceparent does not round-trip: %q", span.traceparent())
	}
}

// The exported document must be an OTLP payload a collector accepts.
func TestOTLPPayloadShape(t *testing.T) {
	span := finishedSpan{
		ctx:        spanContext{traceID: strings.Repeat("a", 32), spanID: strings.Repeat("b", 16), parentID: strings.Repeat("c", 16), sampled: true},
		name:       "POST /api/gateway/decide",
		start:      time.Now(),
		end:        time.Now().Add(3 * time.Millisecond),
		statusCode: 202,
		attributes: map[string]string{"url.path": "/api/gateway/decide"},
	}
	encoded, err := json.Marshal(otlpPayload("promtact", []finishedSpan{span}))
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, needle := range []string{"resourceSpans", "scopeSpans", "traceId", "spanId", "parentSpanId", "startTimeUnixNano", "service.name"} {
		if !strings.Contains(body, needle) {
			t.Errorf("payload is missing %q", needle)
		}
	}
	if otlpStatus(500) != 2 || otlpStatus(200) != 1 {
		t.Error("HTTP status is not mapped onto OTLP status correctly")
	}
}

func TestTracerExportsBatch(t *testing.T) {
	var mu sync.Mutex
	var received []byte
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" {
			t.Errorf("unexpected export path %q", r.URL.Path)
		}
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		mu.Lock()
		received = body
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	tr := newTracer(collector.URL, "promtact-test", nil, 10)
	tr.record(finishedSpan{ctx: newSpanContext(""), name: "GET /api/status", start: time.Now(), end: time.Now(), statusCode: 200})
	if err := tr.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(string(received), "promtact-test") {
		t.Fatalf("collector did not receive the batch: %q", string(received))
	}
}

// Telemetry must degrade rather than grow without bound or block the request
// path: past the queue limit spans are dropped and counted.
func TestTracerDropsBeyondQueueLimit(t *testing.T) {
	tr := newTracer("http://collector.invalid", "promtact", nil, 2)
	for i := 0; i < 5; i++ {
		tr.record(finishedSpan{ctx: newSpanContext(""), name: "x", start: time.Now(), end: time.Now(), statusCode: 200})
	}
	if tr.Dropped() != 3 {
		t.Fatalf("expected 3 dropped spans, got %d", tr.Dropped())
	}
}

// A dead collector must not fail requests or block them.
func TestDeadCollectorDoesNotAffectRequests(t *testing.T) {
	app, err := NewWithOptions(Options{TraceEndpoint: "http://127.0.0.1:1", TraceQueueSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Routes()

	start := time.Now()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("request failed while tracing to a dead collector: %d", rec.Code)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the request waited on the collector: %s", elapsed)
	}
	if rec.Header().Get(traceparentHeader) == "" {
		t.Fatal("a traced response should carry traceparent")
	}
}

// With no endpoint configured nothing is traced and no header is added.
func TestTracingDisabledByDefault(t *testing.T) {
	app, err := NewWithOptions(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if app.tracer.enabled() {
		t.Fatal("tracing must be off unless an endpoint is configured")
	}
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if rec.Header().Get(traceparentHeader) != "" {
		t.Fatal("no traceparent should be emitted when tracing is disabled")
	}
}
