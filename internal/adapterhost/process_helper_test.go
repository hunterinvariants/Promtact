package adapterhost

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/hunterinvariants/promtact/internal/adapterproto"
)

const processHelperModeEnv = "PROMTACT_PROCESS_HELPER_MODE"

func TestAdapterProcessHelper(t *testing.T) {
	mode := os.Getenv(processHelperModeEnv)
	if mode == "" {
		return
	}

	if err := runAdapterProcessHelper(mode); err != nil {
		_, _ = fmt.Fprintf(
			os.Stderr,
			"process helper failed: %v\n",
			err,
		)
		os.Exit(2)
	}

	os.Exit(0)
}

func runAdapterProcessHelper(mode string) error {
	switch mode {
	case "success":
		if _, err := fmt.Fprintln(
			os.Stderr,
			"token-ring adapter ready",
		); err != nil {
			return fmt.Errorf("write startup diagnostic: %w", err)
		}
		return serveSuccessfulProcessAdapter()

	case "handshake-error":
		if _, err := fmt.Fprintln(
			os.Stderr,
			"adapter configuration rejected",
		); err != nil {
			return fmt.Errorf("write rejection diagnostic: %w", err)
		}
		return serveHandshakeError()

	case "stderr-overflow":
		if _, err := io.WriteString(
			os.Stderr,
			strings.Repeat("x", MaxCapturedStderr+4096),
		); err != nil {
			return fmt.Errorf("write oversized diagnostic: %w", err)
		}
		return serveHandshakeError()

	case "blocked-handshake":
		var request adapterproto.Request
		if err := adapterproto.ReadFrame(
			os.Stdin,
			&request,
		); err != nil {
			return fmt.Errorf("read blocked hello: %w", err)
		}
		if err := adapterproto.ValidateRequest(request); err != nil {
			return fmt.Errorf("validate blocked hello: %w", err)
		}
		if request.Op != adapterproto.OpHello {
			return fmt.Errorf(
				"blocked operation = %q, want %q",
				request.Op,
				adapterproto.OpHello,
			)
		}

		_, err := io.Copy(io.Discard, os.Stdin)
		return err

	default:
		return fmt.Errorf("unknown helper mode %q", mode)
	}
}

func serveSuccessfulProcessAdapter() error {
	for {
		var request adapterproto.Request
		if err := adapterproto.ReadFrame(
			os.Stdin,
			&request,
		); err != nil {
			return fmt.Errorf("read request: %w", err)
		}
		if err := adapterproto.ValidateRequest(request); err != nil {
			return fmt.Errorf("validate request: %w", err)
		}

		response := adapterproto.Response{
			Version: adapterproto.Version,
			ID:      request.ID,
			Op:      request.Op,
		}

		switch request.Op {
		case adapterproto.OpHello:
			response.Hello = &adapterproto.HelloResponse{
				Nodes:      []uint32{1, 2},
				Invariants: []string{"at-most-one-token"},
			}

		case adapterproto.OpTick:

		case adapterproto.OpDrain:
			if request.Node != nil && *request.Node == 1 {
				response.Messages = []adapterproto.Message{
					{
						From:    1,
						To:      2,
						Kind:    7,
						Value:   99,
						Payload: []byte("token"),
					},
				}
			}

		case adapterproto.OpDeliver:

		case adapterproto.OpCheck:

		case adapterproto.OpClose:
			if err := adapterproto.WriteFrame(
				os.Stdout,
				response,
			); err != nil {
				return fmt.Errorf("write close response: %w", err)
			}
			return nil
		}

		if err := adapterproto.WriteFrame(
			os.Stdout,
			response,
		); err != nil {
			return fmt.Errorf("write response: %w", err)
		}
	}
}

func serveHandshakeError() error {
	var request adapterproto.Request
	if err := adapterproto.ReadFrame(
		os.Stdin,
		&request,
	); err != nil {
		return fmt.Errorf("read hello: %w", err)
	}
	if err := adapterproto.ValidateRequest(request); err != nil {
		return fmt.Errorf("validate hello: %w", err)
	}

	return adapterproto.WriteFrame(
		os.Stdout,
		adapterproto.Response{
			Version: adapterproto.Version,
			ID:      request.ID,
			Op:      request.Op,
			Error: &adapterproto.RemoteError{
				Code:    "handshake-rejected",
				Message: "adapter cannot initialize",
			},
		},
	)
}
