package adapterhost

import (
	"errors"
	"fmt"
	"net"
	"reflect"
	"testing"

	"github.com/hunterinvariants/promtact/internal/adapterproto"
)

func TestSessionHappyPath(t *testing.T) {
	host, adapter := net.Pipe()
	defer host.Close()

	wantMessage := adapterproto.Message{
		From:    1,
		To:      2,
		Kind:    7,
		Value:   99,
		Payload: []byte("token"),
	}

	done := serveScript(adapter, []scriptStep{
		{
			op: adapterproto.OpHello,
			check: func(request adapterproto.Request) error {
				if request.Hello == nil || request.Hello.Seed != -17 {
					return fmt.Errorf(
						"hello seed = %#v, want -17",
						request.Hello,
					)
				}
				return nil
			},
			respond: func(request adapterproto.Request) adapterproto.Response {
				return successResponse(
					request,
					func(response *adapterproto.Response) {
						response.Hello = &adapterproto.HelloResponse{
							Nodes:      []uint32{1, 2},
							Invariants: []string{"at-most-one-token"},
						}
					},
				)
			},
		},
		{
			op: adapterproto.OpTick,
			check: func(request adapterproto.Request) error {
				if request.Node == nil || *request.Node != 1 {
					return fmt.Errorf("tick node = %v, want 1", request.Node)
				}
				return nil
			},
		},
		{
			op: adapterproto.OpDrain,
			check: func(request adapterproto.Request) error {
				if request.Node == nil || *request.Node != 1 {
					return fmt.Errorf("drain node = %v, want 1", request.Node)
				}
				return nil
			},
			respond: func(request adapterproto.Request) adapterproto.Response {
				return successResponse(
					request,
					func(response *adapterproto.Response) {
						response.Messages = []adapterproto.Message{
							wantMessage,
						}
					},
				)
			},
		},
		{
			op: adapterproto.OpDeliver,
			check: func(request adapterproto.Request) error {
				if request.Node == nil || *request.Node != 2 {
					return fmt.Errorf("deliver node = %v, want 2", request.Node)
				}
				if request.Message == nil ||
					!reflect.DeepEqual(*request.Message, wantMessage) {
					return fmt.Errorf(
						"delivered message = %#v, want %#v",
						request.Message,
						wantMessage,
					)
				}
				return nil
			},
		},
		{
			op: adapterproto.OpCheck,
			respond: func(request adapterproto.Request) adapterproto.Response {
				return successResponse(
					request,
					func(response *adapterproto.Response) {
						response.Violation = &adapterproto.Violation{
							Invariant: "at-most-one-token",
							Detail:    "token held by nodes [1 2]",
						}
					},
				)
			},
		},
		{
			op: adapterproto.OpClose,
		},
	})

	session, err := Open(host, host, -17)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	nodes := session.Nodes()
	if !reflect.DeepEqual(nodes, []uint32{1, 2}) {
		t.Fatalf("Nodes() = %v, want [1 2]", nodes)
	}
	nodes[0] = 99
	if got := session.Nodes(); !reflect.DeepEqual(got, []uint32{1, 2}) {
		t.Fatalf("Nodes() changed through returned slice: %v", got)
	}

	invariants := session.Invariants()
	if !reflect.DeepEqual(invariants, []string{"at-most-one-token"}) {
		t.Fatalf(
			"Invariants() = %v, want [at-most-one-token]",
			invariants,
		)
	}
	invariants[0] = "changed"
	if got := session.Invariants(); !reflect.DeepEqual(
		got,
		[]string{"at-most-one-token"},
	) {
		t.Fatalf("Invariants() changed through returned slice: %v", got)
	}

	if err := session.Tick(1); err != nil {
		t.Fatalf("Tick() failed: %v", err)
	}

	messages, err := session.Drain(1)
	if err != nil {
		t.Fatalf("Drain() failed: %v", err)
	}
	if !reflect.DeepEqual(messages, []adapterproto.Message{wantMessage}) {
		t.Fatalf("Drain() = %#v, want %#v", messages, wantMessage)
	}

	if err := session.Deliver(2, messages[0]); err != nil {
		t.Fatalf("Deliver() failed: %v", err)
	}

	violation, err := session.Check()
	if err != nil {
		t.Fatalf("Check() failed: %v", err)
	}
	if violation == nil ||
		violation.Invariant != "at-most-one-token" ||
		violation.Detail != "token held by nodes [1 2]" {
		t.Fatalf("Check() = %#v, want declared violation", violation)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close() failed: %v", err)
	}
	if err := session.Tick(1); !errors.Is(err, ErrClosed) {
		t.Fatalf("Tick() after Close() = %v, want %v", err, ErrClosed)
	}

	if err := <-done; err != nil {
		t.Fatalf("scripted adapter failed: %v", err)
	}
}

type scriptStep struct {
	op      adapterproto.Operation
	check   func(adapterproto.Request) error
	respond func(adapterproto.Request) adapterproto.Response
}

func serveScript(
	connection net.Conn,
	steps []scriptStep,
) <-chan error {
	done := make(chan error, 1)

	go func() {
		defer connection.Close()

		for index, step := range steps {
			var request adapterproto.Request
			if err := adapterproto.ReadFrame(
				connection,
				&request,
			); err != nil {
				done <- fmt.Errorf(
					"step %d read request: %w",
					index,
					err,
				)
				return
			}
			if err := adapterproto.ValidateRequest(request); err != nil {
				done <- fmt.Errorf(
					"step %d validate request: %w",
					index,
					err,
				)
				return
			}

			wantID := uint64(index + 1)
			if request.ID != wantID {
				done <- fmt.Errorf(
					"step %d request id = %d, want %d",
					index,
					request.ID,
					wantID,
				)
				return
			}
			if request.Op != step.op {
				done <- fmt.Errorf(
					"step %d operation = %q, want %q",
					index,
					request.Op,
					step.op,
				)
				return
			}
			if step.check != nil {
				if err := step.check(request); err != nil {
					done <- fmt.Errorf(
						"step %d request: %w",
						index,
						err,
					)
					return
				}
			}

			response := successResponse(request, nil)
			if step.respond != nil {
				response = step.respond(request)
			}
			if err := adapterproto.WriteFrame(
				connection,
				response,
			); err != nil {
				done <- fmt.Errorf(
					"step %d write response: %w",
					index,
					err,
				)
				return
			}
		}

		done <- nil
	}()

	return done
}

func successResponse(
	request adapterproto.Request,
	mutate func(*adapterproto.Response),
) adapterproto.Response {
	response := adapterproto.Response{
		Version: adapterproto.Version,
		ID:      request.ID,
		Op:      request.Op,
	}
	if mutate != nil {
		mutate(&response)
	}
	return response
}
