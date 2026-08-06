package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Distributed tracing over OTLP/HTTP, spoken directly against any OpenTelemetry
// collector, Tempo or Jaeger endpoint.
//
// The wire format is implemented here rather than pulling in the OpenTelemetry
// SDK on purpose: that SDK brings on the order of twenty modules including
// protobuf and gRPC, which would multiply the dependency surface of a product
// whose argument is a lean, auditable supply chain — for a feature that is off
// by default. OTLP's JSON encoding is a documented, stable protocol, so speaking
// it with the standard library costs interoperability nothing.
//
// Tracing is disabled unless an endpoint is configured. Export is asynchronous
// and bounded: a slow or dead collector must never add latency to an enforcement
// decision, and must never grow memory without limit.

const traceparentHeader = "traceparent"

type spanContext struct {
	traceID  string // 32 hex chars
	spanID   string // 16 hex chars
	parentID string
	sampled  bool
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// A degraded id is better than dropping the trace; it stays unique
		// enough for correlation within a run.
		return hex.EncodeToString([]byte(time.Now().UTC().Format("150405.000000000")))[:n*2]
	}
	return hex.EncodeToString(buf)
}

// parseTraceparent reads the W3C Trace Context header so a caller's trace
// continues through the gateway instead of starting a disconnected one.
func parseTraceparent(value string) (traceID string, parentSpanID string, sampled bool, ok bool) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 4 || parts[0] != "00" {
		return "", "", false, false
	}
	if len(parts[1]) != 32 || len(parts[2]) != 16 {
		return "", "", false, false
	}
	if !isHex(parts[1]) || !isHex(parts[2]) {
		return "", "", false, false
	}
	if strings.Trim(parts[1], "0") == "" || strings.Trim(parts[2], "0") == "" {
		return "", "", false, false // all-zero ids are invalid per the spec
	}
	flags := parts[3]
	return parts[1], parts[2], strings.HasSuffix(flags, "1"), true
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil
}

func newSpanContext(incoming string) spanContext {
	if traceID, parentID, sampled, ok := parseTraceparent(incoming); ok {
		return spanContext{traceID: traceID, spanID: randomHex(8), parentID: parentID, sampled: sampled}
	}
	return spanContext{traceID: randomHex(16), spanID: randomHex(8), sampled: true}
}

func (s spanContext) traceparent() string {
	flags := "00"
	if s.sampled {
		flags = "01"
	}
	return fmt.Sprintf("00-%s-%s-%s", s.traceID, s.spanID, flags)
}

type finishedSpan struct {
	ctx        spanContext
	name       string
	start      time.Time
	end        time.Time
	statusCode int
	attributes map[string]string
}

// tracer batches spans and ships them to an OTLP/HTTP endpoint.
type tracer struct {
	endpoint    string
	serviceName string
	headers     map[string]string
	client      *http.Client

	mu      sync.Mutex
	pending []finishedSpan
	max     int
	dropped int
}

func newTracer(endpoint string, serviceName string, headers map[string]string, max int) *tracer {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return nil
	}
	if max <= 0 {
		max = 2048
	}
	if serviceName == "" {
		serviceName = "promtact"
	}
	return &tracer{
		endpoint:    endpoint + "/v1/traces",
		serviceName: serviceName,
		headers:     headers,
		client:      &http.Client{Timeout: 10 * time.Second},
		max:         max,
	}
}

func (t *tracer) enabled() bool { return t != nil && t.endpoint != "" }

// record queues a span. When the queue is full the span is dropped and counted:
// telemetry must degrade rather than block the request path or exhaust memory.
func (t *tracer) record(span finishedSpan) {
	if !t.enabled() || !span.ctx.sampled {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.pending) >= t.max {
		t.dropped++
		return
	}
	t.pending = append(t.pending, span)
}

func (t *tracer) Dropped() int {
	if !t.enabled() {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dropped
}

func (t *tracer) take() []finishedSpan {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.pending) == 0 {
		return nil
	}
	batch := t.pending
	t.pending = nil
	return batch
}

// Flush ships whatever is queued. Safe to call when nothing is pending.
func (t *tracer) Flush(ctx context.Context) error {
	if !t.enabled() {
		return nil
	}
	batch := t.take()
	if len(batch) == 0 {
		return nil
	}
	payload, err := json.Marshal(otlpPayload(t.serviceName, batch))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range t.headers {
		req.Header.Set(key, value)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("trace export returned %s", resp.Status)
	}
	return nil
}

// StartTraceExporter flushes on an interval until the context is cancelled.
func (a *App) StartTraceExporter(ctx context.Context, every time.Duration) {
	if !a.tracer.enabled() {
		return
	}
	if every <= 0 {
		every = 5 * time.Second
	}
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				if err := a.tracer.Flush(flushCtx); err != nil {
					log.Printf("final trace flush failed: %s", sanitizeLogValue(err.Error()))
				}
				cancel()
				return
			case <-ticker.C:
				if err := a.tracer.Flush(ctx); err != nil {
					log.Printf("trace export failed: %s", sanitizeLogValue(err.Error()))
				}
			}
		}
	}()
}

func otlpPayload(serviceName string, spans []finishedSpan) map[string]any {
	encoded := make([]map[string]any, 0, len(spans))
	for _, span := range spans {
		attributes := make([]map[string]any, 0, len(span.attributes))
		for key, value := range span.attributes {
			attributes = append(attributes, map[string]any{
				"key":   key,
				"value": map[string]any{"stringValue": value},
			})
		}
		entry := map[string]any{
			"traceId":           span.ctx.traceID,
			"spanId":            span.ctx.spanID,
			"name":              span.name,
			"kind":              2, // SPAN_KIND_SERVER
			"startTimeUnixNano": fmt.Sprintf("%d", span.start.UnixNano()),
			"endTimeUnixNano":   fmt.Sprintf("%d", span.end.UnixNano()),
			"attributes":        attributes,
			"status":            map[string]any{"code": otlpStatus(span.statusCode)},
		}
		if span.ctx.parentID != "" {
			entry["parentSpanId"] = span.ctx.parentID
		}
		encoded = append(encoded, entry)
	}

	return map[string]any{
		"resourceSpans": []map[string]any{{
			"resource": map[string]any{
				"attributes": []map[string]any{
					{"key": "service.name", "value": map[string]any{"stringValue": serviceName}},
					{"key": "service.version", "value": map[string]any{"stringValue": Version}},
				},
			},
			"scopeSpans": []map[string]any{{
				"scope": map[string]any{"name": "promtact/server"},
				"spans": encoded,
			}},
		}},
	}
}

// otlpStatus maps HTTP status onto OTLP status codes: 1 = Ok, 2 = Error.
func otlpStatus(status int) int {
	if status >= 500 {
		return 2
	}
	return 1
}
