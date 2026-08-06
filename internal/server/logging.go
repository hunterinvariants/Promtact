package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// Every request carries a correlation id so a single agent decision can be
// followed across the access log, the audit record and the caller's own logs.
// For a security product that trail is the difference between "something was
// blocked" and being able to reconstruct what happened.

type correlationContextKey struct{}
type requestInfoKey struct{}

// requestInfo lets the authentication layer report who the caller turned out to
// be back to the logging middleware, which wraps it from the outside so that
// rejected requests are logged too.
type requestInfo struct {
	principalName string
	tenant        string
}

func notePrincipal(r *http.Request, name string, tenant string) {
	if info, ok := r.Context().Value(requestInfoKey{}).(*requestInfo); ok {
		info.principalName = name
		info.tenant = tenant
	}
}

const correlationHeader = "X-Correlation-Id"

// maxCorrelationIDLength bounds what a client may supply.
const maxCorrelationIDLength = 64

// sanitizeCorrelationID accepts a caller-supplied id only if it is short and
// alphanumeric. The id is written into logs, so an unvalidated one would let a
// caller forge log lines or smuggle control characters into them.
func sanitizeCorrelationID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxCorrelationIDLength {
		return ""
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return ""
		}
	}
	return value
}

func newCorrelationID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "req-" + time.Now().UTC().Format("20060102150405.000000000")
	}
	return "req-" + hex.EncodeToString(buf)
}

// CorrelationID returns the id assigned to this request.
func CorrelationID(r *http.Request) string {
	if r == nil {
		return ""
	}
	if value, ok := r.Context().Value(correlationContextKey{}).(string); ok {
		return value
	}
	return ""
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

type requestLogLine struct {
	Time          string  `json:"time"`
	TraceID       string  `json:"trace_id,omitempty"`
	SpanID        string  `json:"span_id,omitempty"`
	Level         string  `json:"level"`
	Message       string  `json:"msg"`
	CorrelationID string  `json:"correlation_id"`
	Method        string  `json:"method"`
	Path          string  `json:"path"`
	Status        int     `json:"status"`
	DurationMS    float64 `json:"duration_ms"`
	Bytes         int     `json:"bytes,omitempty"`
	Principal     string  `json:"principal,omitempty"`
	Tenant        string  `json:"tenant,omitempty"`
	RemoteIP      string  `json:"remote_ip,omitempty"`
}

// withRequestLogging assigns a correlation id, echoes it back, and emits one
// structured line per request. Query strings and headers are deliberately not
// logged: they carry tokens.
func (a *App) withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeCorrelationID(r.Header.Get(correlationHeader))
		if id == "" {
			id = sanitizeCorrelationID(r.Header.Get("X-Request-Id"))
		}
		if id == "" {
			id = newCorrelationID()
		}

		info := &requestInfo{}
		ctx := context.WithValue(r.Context(), correlationContextKey{}, id)
		ctx = context.WithValue(ctx, requestInfoKey{}, info)
		r = r.WithContext(ctx)
		w.Header().Set(correlationHeader, id)

		// A caller's trace continues through the gateway rather than starting a
		// disconnected one, and the same identifiers appear in the log line.
		span := newSpanContext(r.Header.Get(traceparentHeader))
		if a.tracer.enabled() {
			w.Header().Set(traceparentHeader, span.traceparent())
		}

		recorder := &statusRecorder{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(recorder, r)
		elapsed := time.Since(start)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}

		if a.tracer.enabled() {
			a.tracer.record(finishedSpan{
				ctx:        span,
				name:       r.Method + " " + sanitizeLogValue(r.URL.Path),
				start:      start,
				end:        start.Add(elapsed),
				statusCode: recorder.status,
				attributes: map[string]string{
					"http.request.method":       r.Method,
					"url.path":                  sanitizeLogValue(r.URL.Path),
					"http.response.status_code": fmt.Sprintf("%d", recorder.status),
					"promtact.correlation_id":      id,
					"promtact.tenant":              sanitizeLogValue(info.tenant),
				},
			})
		}

		if !a.structuredLogs {
			return
		}

		line := requestLogLine{
			Time:          time.Now().UTC().Format(time.RFC3339Nano),
			TraceID:       traceIDIfEnabled(a, span),
			SpanID:        spanIDIfEnabled(a, span),
			Level:         logLevelForStatus(recorder.status),
			Message:       "http_request",
			CorrelationID: id,
			Method:        r.Method,
			Path:          sanitizeLogValue(r.URL.Path),
			Status:        recorder.status,
			DurationMS:    float64(elapsed.Microseconds()) / 1000,
			Bytes:         recorder.bytes,
			Principal:     sanitizeLogValue(info.principalName),
			Tenant:        sanitizeLogValue(info.tenant),
			RemoteIP:      sanitizeLogValue(a.sourceIP(r)),
		}
		if encoded, err := json.Marshal(line); err == nil {
			// Written directly so the JSON object is the whole line, without the
			// standard logger's timestamp prefix in front of it.
			_, _ = os.Stderr.Write(append(encoded, '\n'))
		} else {
			log.Printf("could not encode request log line: %v", err)
		}
	})
}

func logLevelForStatus(status int) string {
	switch {
	case status >= 500:
		return "error"
	case status >= 400:
		return "warn"
	default:
		return "info"
	}
}

// traceIDIfEnabled keeps trace identifiers out of the log line when tracing is
// off, so operators are not shown ids that lead to nothing.
func traceIDIfEnabled(a *App, span spanContext) string {
	if a.tracer.enabled() {
		return span.traceID
	}
	return ""
}

func spanIDIfEnabled(a *App, span spanContext) string {
	if a.tracer.enabled() {
		return span.spanID
	}
	return ""
}
