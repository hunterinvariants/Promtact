package adapterproto

import (
	"fmt"
	"strings"
)

func ValidateRequest(request Request) error {
	if err := validateEnvelope(request.Version, request.ID, request.Op); err != nil {
		return err
	}

	switch request.Op {
	case OpHello:
		if request.Hello == nil {
			return protocolError("hello request has no hello body")
		}
		if request.Node != nil || request.Message != nil {
			return protocolError("hello request has unrelated fields")
		}
	case OpTick, OpDrain:
		if request.Node == nil {
			return protocolError("%s request has no node", request.Op)
		}
		if request.Hello != nil || request.Message != nil {
			return protocolError("%s request has unrelated fields", request.Op)
		}
	case OpDeliver:
		if request.Node == nil || request.Message == nil {
			return protocolError("deliver request requires node and message")
		}
		if request.Hello != nil {
			return protocolError("deliver request has unrelated fields")
		}
		if *request.Node != request.Message.To {
			return protocolError(
				"deliver node %d does not match message destination %d",
				*request.Node,
				request.Message.To,
			)
		}
	case OpCheck, OpClose:
		if request.Hello != nil || request.Node != nil || request.Message != nil {
			return protocolError("%s request has unrelated fields", request.Op)
		}
	default:
		return protocolError("unknown operation %q", request.Op)
	}

	return nil
}

func ValidateResponse(response Response) error {
	if err := validateEnvelope(response.Version, response.ID, response.Op); err != nil {
		return err
	}

	if response.Error != nil {
		if strings.TrimSpace(response.Error.Code) == "" {
			return protocolError("remote error has no code")
		}
		if strings.TrimSpace(response.Error.Message) == "" {
			return protocolError("remote error has no message")
		}
		if response.Hello != nil || len(response.Messages) != 0 || response.Violation != nil {
			return protocolError("error response also contains success fields")
		}
		return nil
	}

	switch response.Op {
	case OpHello:
		if response.Hello == nil {
			return protocolError("hello response has no hello body")
		}
		if len(response.Messages) != 0 || response.Violation != nil {
			return protocolError("hello response has unrelated fields")
		}
		if err := validateHello(response.Hello); err != nil {
			return err
		}
	case OpTick, OpDeliver, OpClose:
		if response.Hello != nil || len(response.Messages) != 0 || response.Violation != nil {
			return protocolError("%s response has unrelated fields", response.Op)
		}
	case OpDrain:
		if response.Hello != nil || response.Violation != nil {
			return protocolError("drain response has unrelated fields")
		}
	case OpCheck:
		if response.Hello != nil || len(response.Messages) != 0 {
			return protocolError("check response has unrelated fields")
		}
		if response.Violation != nil {
			if strings.TrimSpace(response.Violation.Invariant) == "" {
				return protocolError("violation has no invariant name")
			}
			if strings.TrimSpace(response.Violation.Detail) == "" {
				return protocolError("violation has no detail")
			}
		}
	default:
		return protocolError("unknown operation %q", response.Op)
	}

	return nil
}

func ValidateResponseFor(request Request, response Response) error {
	if err := ValidateRequest(request); err != nil {
		return fmt.Errorf("request: %w", err)
	}
	if err := ValidateResponse(response); err != nil {
		return fmt.Errorf("response: %w", err)
	}
	if response.ID != request.ID {
		return protocolError(
			"response id %d does not match request id %d",
			response.ID,
			request.ID,
		)
	}
	if response.Op != request.Op {
		return protocolError(
			"response operation %q does not match request operation %q",
			response.Op,
			request.Op,
		)
	}
	if response.Error == nil && request.Op == OpDrain {
		for index, message := range response.Messages {
			if message.From != *request.Node {
				return protocolError(
					"drained message %d has sender %d, want node %d",
					index,
					message.From,
					*request.Node,
				)
			}
		}
	}
	return nil
}

func validateEnvelope(version uint16, id uint64, operation Operation) error {
	if version != Version {
		return protocolError("version %d is not supported", version)
	}
	if id == 0 {
		return protocolError("request id must be non-zero")
	}
	if operation == "" {
		return protocolError("operation is empty")
	}
	switch operation {
	case OpHello, OpTick, OpDrain, OpDeliver, OpCheck, OpClose:
		return nil
	default:
		return protocolError("unknown operation %q", operation)
	}
}

func validateHello(hello *HelloResponse) error {
	if len(hello.Nodes) == 0 {
		return protocolError("hello response has no nodes")
	}

	nodes := make(map[uint32]struct{}, len(hello.Nodes))
	for _, node := range hello.Nodes {
		if _, exists := nodes[node]; exists {
			return protocolError("hello response repeats node %d", node)
		}
		nodes[node] = struct{}{}
	}

	invariants := make(map[string]struct{}, len(hello.Invariants))
	for _, invariant := range hello.Invariants {
		if strings.TrimSpace(invariant) == "" {
			return protocolError("hello response has an empty invariant name")
		}
		if _, exists := invariants[invariant]; exists {
			return protocolError(
				"hello response repeats invariant %q",
				invariant,
			)
		}
		invariants[invariant] = struct{}{}
	}

	return nil
}

func protocolError(format string, arguments ...any) error {
	return fmt.Errorf("adapter protocol: "+format, arguments...)
}
