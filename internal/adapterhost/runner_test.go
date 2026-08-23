package adapterhost

import (
	"fmt"
	"net"
	"reflect"
	"testing"

	"github.com/hunterinvariants/promtact/dst"
	"github.com/hunterinvariants/promtact/internal/adapterproto"
)

func TestRunnerDeterministicCampaign(t *testing.T) {
	first := runPingCampaign(t, 0x4a2c)
	second := runPingCampaign(t, 0x4a2c)

	if first.trace != second.trace {
		t.Fatalf(
			"trace mismatch: first %s, second %s",
			first.trace,
			second.trace,
		)
	}
	if !reflect.DeepEqual(first.drops, second.drops) {
		t.Fatalf(
			"drop mismatch: first %v, second %v",
			first.drops,
			second.drops,
		)
	}
	if first.pending != second.pending {
		t.Fatalf(
			"pending mismatch: first %d, second %d",
			first.pending,
			second.pending,
		)
	}
	if first.delivered != second.delivered {
		t.Fatalf(
			"delivery mismatch: first %d, second %d",
			first.delivered,
			second.delivered,
		)
	}
	if first.now != 24 || second.now != 24 {
		t.Fatalf(
			"virtual time = %d/%d, want 24/24",
			first.now,
			second.now,
		)
	}

	totalDrops := 0
	for _, count := range first.drops {
		totalDrops += count
	}
	if totalDrops == 0 {
		t.Fatal("configured partition dropped no messages")
	}
	if first.delivered == 0 {
		t.Fatal("campaign delivered no messages")
	}
	if first.trace == fmt.Sprintf("%064x", 0) {
		t.Fatal("campaign produced the empty trace")
	}
}

type pingCampaignResult struct {
	trace     string
	drops     map[string]int
	pending   int
	delivered int
	now       uint64
}

func runPingCampaign(t *testing.T, seed int64) pingCampaignResult {
	t.Helper()

	host, adapter := net.Pipe()
	defer host.Close()

	done := servePingAdapter(adapter)

	session, err := Open(host, host, seed)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	runner, err := NewRunner(dst.Config{
		Seed:     seed,
		MaxDelay: 3,
	}, session)
	if err != nil {
		t.Fatalf("NewRunner() failed: %v", err)
	}

	runner.Inject(
		dst.During(
			4,
			16,
			dst.Split(
				[]uint32{1},
				[]uint32{2},
			),
		),
	)

	if err := runner.RunChecked(24); err != nil {
		t.Fatalf("RunChecked() failed: %v", err)
	}

	result := pingCampaignResult{
		trace:   runner.TraceHash(),
		drops:   runner.InjectedDrops(),
		pending: runner.Pending(),
		now:     runner.Now(),
	}

	if err := runner.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	adapterResult := <-done
	if adapterResult.err != nil {
		t.Fatalf("ping adapter failed: %v", adapterResult.err)
	}
	result.delivered = adapterResult.delivered

	return result
}

type pingAdapterResult struct {
	delivered int
	err       error
}

func servePingAdapter(
	connection net.Conn,
) <-chan pingAdapterResult {
	done := make(chan pingAdapterResult, 1)

	go func() {
		defer connection.Close()

		outbound := map[uint32][]adapterproto.Message{
			1: nil,
			2: nil,
		}
		nextID := uint64(1)
		messageValue := uint64(0)
		delivered := 0

		for {
			var request adapterproto.Request
			if err := adapterproto.ReadFrame(
				connection,
				&request,
			); err != nil {
				done <- pingAdapterResult{
					delivered: delivered,
					err:       fmt.Errorf("read request: %w", err),
				}
				return
			}
			if err := adapterproto.ValidateRequest(request); err != nil {
				done <- pingAdapterResult{
					delivered: delivered,
					err:       fmt.Errorf("validate request: %w", err),
				}
				return
			}
			if request.ID != nextID {
				done <- pingAdapterResult{
					delivered: delivered,
					err: fmt.Errorf(
						"request id = %d, want %d",
						request.ID,
						nextID,
					),
				}
				return
			}
			nextID++

			response := successResponse(request, nil)

			switch request.Op {
			case adapterproto.OpHello:
				response.Hello = &adapterproto.HelloResponse{
					Nodes:      []uint32{1, 2},
					Invariants: []string{"state-valid"},
				}

			case adapterproto.OpTick:
				from := *request.Node
				to := uint32(1)
				if from == 1 {
					to = 2
				}

				messageValue++
				outbound[from] = append(
					outbound[from],
					adapterproto.Message{
						From:    from,
						To:      to,
						Kind:    1,
						Value:   messageValue,
						Payload: []byte{byte(messageValue)},
					},
				)

			case adapterproto.OpDrain:
				node := *request.Node
				response.Messages = append(
					[]adapterproto.Message(nil),
					outbound[node]...,
				)
				outbound[node] = outbound[node][:0]

			case adapterproto.OpDeliver:
				delivered++

			case adapterproto.OpCheck:
				// The test adapter has no mutable safety state.

			case adapterproto.OpClose:
				if err := adapterproto.WriteFrame(
					connection,
					response,
				); err != nil {
					done <- pingAdapterResult{
						delivered: delivered,
						err:       fmt.Errorf("write close: %w", err),
					}
					return
				}
				done <- pingAdapterResult{
					delivered: delivered,
				}
				return
			}

			if err := adapterproto.ValidateResponseFor(
				request,
				response,
			); err != nil {
				done <- pingAdapterResult{
					delivered: delivered,
					err:       fmt.Errorf("validate response: %w", err),
				}
				return
			}
			if err := adapterproto.WriteFrame(
				connection,
				response,
			); err != nil {
				done <- pingAdapterResult{
					delivered: delivered,
					err:       fmt.Errorf("write response: %w", err),
				}
				return
			}
		}
	}()

	return done
}
