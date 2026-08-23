package adapterhost

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/internal/adapterproto"
)

func TestOpenRequiresBothStreams(t *testing.T) {
	if _, err := Open(nil, io.Discard, 1); err == nil {
		t.Fatal("Open(nil reader) succeeded, want error")
	}
	if _, err := Open(bytes.NewReader(nil), nil, 1); err == nil {
		t.Fatal("Open(nil writer) succeeded, want error")
	}
}

func TestOpenReturnsTypedRemoteError(t *testing.T) {
	host, adapter := net.Pipe()
	defer host.Close()

	done := serveScript(adapter, []scriptStep{
		{
			op: adapterproto.OpHello,
			respond: func(request adapterproto.Request) adapterproto.Response {
				return adapterproto.Response{
					Version: adapterproto.Version,
					ID:      request.ID,
					Op:      request.Op,
					Error: &adapterproto.RemoteError{
						Code:    "unsupported",
						Message: "adapter cannot initialize",
					},
				}
			},
		},
	})

	_, err := Open(host, host, 7)
	if err == nil {
		t.Fatal("Open() succeeded, want remote error")
	}

	var remote *RemoteError
	if !errors.As(err, &remote) {
		t.Fatalf("Open() error = %T(%v), want *RemoteError", err, err)
	}
	if remote.Operation != adapterproto.OpHello ||
		remote.Code != "unsupported" ||
		remote.Message != "adapter cannot initialize" {
		t.Fatalf("remote error = %#v, want hello/unsupported", remote)
	}

	if err := <-done; err != nil {
		t.Fatalf("scripted adapter failed: %v", err)
	}
}

func TestOpenRejectsMismatchedResponseID(t *testing.T) {
	host, adapter := net.Pipe()
	defer host.Close()

	done := serveScript(adapter, []scriptStep{
		{
			op: adapterproto.OpHello,
			respond: func(request adapterproto.Request) adapterproto.Response {
				response := successResponse(
					request,
					func(response *adapterproto.Response) {
						response.Hello = &adapterproto.HelloResponse{
							Nodes: []uint32{1},
						}
					},
				)
				response.ID++
				return response
			},
		},
	})

	_, err := Open(host, host, 8)
	if err == nil ||
		!strings.Contains(err.Error(), "response id") {
		t.Fatalf("Open() error = %v, want response-id mismatch", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("scripted adapter failed: %v", err)
	}
}

func TestOpenRejectsTruncatedResponse(t *testing.T) {
	host, adapter := net.Pipe()
	defer host.Close()

	done := make(chan error, 1)
	go func() {
		defer adapter.Close()

		var request adapterproto.Request
		if err := adapterproto.ReadFrame(adapter, &request); err != nil {
			done <- fmt.Errorf("read hello request: %w", err)
			return
		}
		if request.Op != adapterproto.OpHello {
			done <- fmt.Errorf(
				"operation = %q, want %q",
				request.Op,
				adapterproto.OpHello,
			)
			return
		}
		if _, err := adapter.Write([]byte{0, 0}); err != nil {
			done <- fmt.Errorf("write truncated response: %w", err)
			return
		}
		done <- nil
	}()

	_, err := Open(host, host, 9)
	if err == nil ||
		!errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Open() error = %v, want %v", err, io.ErrUnexpectedEOF)
	}

	if err := <-done; err != nil {
		t.Fatalf("truncated adapter failed: %v", err)
	}
}

func TestSessionRejectsExhaustedRequestIDs(t *testing.T) {
	session := &Session{
		reader:       bytes.NewReader(nil),
		writer:       io.Discard,
		nextID:       0,
		nodeSet:      map[uint32]struct{}{1: {}},
		invariantSet: make(map[string]struct{}),
	}

	if _, err := session.Check(); !errors.Is(err, ErrIDExhausted) {
		t.Fatalf("Check() error = %v, want %v", err, ErrIDExhausted)
	}
}
