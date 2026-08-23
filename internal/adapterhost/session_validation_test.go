package adapterhost

import (
	"net"
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/internal/adapterproto"
)

func TestSessionEnforcesHandshakeBoundary(t *testing.T) {
	host, adapter := net.Pipe()
	defer host.Close()

	done := serveScript(adapter, []scriptStep{
		{
			op: adapterproto.OpHello,
			respond: func(request adapterproto.Request) adapterproto.Response {
				return successResponse(
					request,
					func(response *adapterproto.Response) {
						response.Hello = &adapterproto.HelloResponse{
							Nodes:      []uint32{1, 2},
							Invariants: []string{"declared-safety"},
						}
					},
				)
			},
		},
		{
			op: adapterproto.OpDrain,
			respond: func(request adapterproto.Request) adapterproto.Response {
				return successResponse(
					request,
					func(response *adapterproto.Response) {
						response.Messages = []adapterproto.Message{
							{
								From:  1,
								To:    9,
								Kind:  1,
								Value: 2,
							},
						}
					},
				)
			},
		},
		{
			op: adapterproto.OpCheck,
			respond: func(request adapterproto.Request) adapterproto.Response {
				return successResponse(
					request,
					func(response *adapterproto.Response) {
						response.Violation = &adapterproto.Violation{
							Invariant: "undeclared-safety",
							Detail:    "failed",
						}
					},
				)
			},
		},
		{
			op: adapterproto.OpClose,
		},
	})

	session, err := Open(host, host, 11)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	if err := session.Tick(9); err == nil ||
		!strings.Contains(err.Error(), "node 9 is not configured") {
		t.Fatalf("Tick(9) error = %v, want unknown-node error", err)
	}

	if _, err := session.Drain(9); err == nil ||
		!strings.Contains(err.Error(), "node 9 is not configured") {
		t.Fatalf("Drain(9) error = %v, want unknown-node error", err)
	}

	if err := session.Deliver(2, adapterproto.Message{
		From: 9,
		To:   2,
	}); err == nil ||
		!strings.Contains(err.Error(), "sender") {
		t.Fatalf(
			"Deliver() with unknown sender error = %v, want sender error",
			err,
		)
	}

	if err := session.Deliver(1, adapterproto.Message{
		From: 2,
		To:   2,
	}); err == nil ||
		!strings.Contains(err.Error(), "does not match node") {
		t.Fatalf(
			"Deliver() with wrong destination error = %v, want mismatch",
			err,
		)
	}

	if _, err := session.Drain(1); err == nil ||
		!strings.Contains(err.Error(), "destination") ||
		!strings.Contains(err.Error(), "node 9 is not configured") {
		t.Fatalf(
			"Drain() with unknown destination error = %v, want boundary error",
			err,
		)
	}

	if _, err := session.Check(); err == nil ||
		!strings.Contains(err.Error(), "undeclared invariant") {
		t.Fatalf(
			"Check() with undeclared invariant error = %v, want declaration error",
			err,
		)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("scripted adapter failed: %v", err)
	}
}

func TestSessionCheckWithoutViolation(t *testing.T) {
	host, adapter := net.Pipe()
	defer host.Close()

	done := serveScript(adapter, []scriptStep{
		{
			op: adapterproto.OpHello,
			respond: func(request adapterproto.Request) adapterproto.Response {
				return successResponse(
					request,
					func(response *adapterproto.Response) {
						response.Hello = &adapterproto.HelloResponse{
							Nodes:      []uint32{1},
							Invariants: []string{"safety"},
						}
					},
				)
			},
		},
		{
			op: adapterproto.OpCheck,
		},
		{
			op: adapterproto.OpClose,
		},
	})

	session, err := Open(host, host, 12)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	violation, err := session.Check()
	if err != nil {
		t.Fatalf("Check() failed: %v", err)
	}
	if violation != nil {
		t.Fatalf("Check() = %#v, want nil", violation)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("scripted adapter failed: %v", err)
	}
}
