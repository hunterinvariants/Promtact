package adapterproto

import "testing"

func TestValidateRequestAcceptsEveryOperation(t *testing.T) {
	nodeZero := uint32(0)
	nodeSeven := uint32(7)

	tests := []struct {
		name    string
		request Request
	}{
		{
			name: "hello",
			request: Request{
				Version: Version,
				ID:      1,
				Op:      OpHello,
				Hello:   &HelloRequest{Seed: -42},
			},
		},
		{
			name: "tick with node zero",
			request: Request{
				Version: Version,
				ID:      2,
				Op:      OpTick,
				Node:    &nodeZero,
			},
		},
		{
			name: "drain",
			request: Request{
				Version: Version,
				ID:      3,
				Op:      OpDrain,
				Node:    &nodeSeven,
			},
		},
		{
			name: "deliver",
			request: Request{
				Version: Version,
				ID:      4,
				Op:      OpDeliver,
				Node:    &nodeSeven,
				Message: &Message{
					From:    3,
					To:      7,
					Kind:    1,
					Value:   9,
					Payload: []byte("payload"),
				},
			},
		},
		{
			name: "check",
			request: Request{
				Version: Version,
				ID:      5,
				Op:      OpCheck,
			},
		},
		{
			name: "close",
			request: Request{
				Version: Version,
				ID:      6,
				Op:      OpClose,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateRequest(test.request); err != nil {
				t.Fatalf("ValidateRequest() failed: %v", err)
			}
		})
	}
}

func TestValidateRequestRejectsInvalidShapes(t *testing.T) {
	nodeOne := uint32(1)
	nodeTwo := uint32(2)
	messageToTwo := &Message{From: 1, To: 2}

	tests := []struct {
		name    string
		request Request
	}{
		{
			name: "unsupported version",
			request: Request{
				Version: Version + 1,
				ID:      1,
				Op:      OpCheck,
			},
		},
		{
			name: "zero request id",
			request: Request{
				Version: Version,
				Op:      OpCheck,
			},
		},
		{
			name: "empty operation",
			request: Request{
				Version: Version,
				ID:      1,
			},
		},
		{
			name: "unknown operation",
			request: Request{
				Version: Version,
				ID:      1,
				Op:      Operation("unknown"),
			},
		},
		{
			name: "hello without body",
			request: Request{
				Version: Version,
				ID:      1,
				Op:      OpHello,
			},
		},
		{
			name: "hello with node",
			request: Request{
				Version: Version,
				ID:      1,
				Op:      OpHello,
				Hello:   &HelloRequest{},
				Node:    &nodeOne,
			},
		},
		{
			name: "tick without node",
			request: Request{
				Version: Version,
				ID:      1,
				Op:      OpTick,
			},
		},
		{
			name: "tick with message",
			request: Request{
				Version: Version,
				ID:      1,
				Op:      OpTick,
				Node:    &nodeOne,
				Message: messageToTwo,
			},
		},
		{
			name: "drain without node",
			request: Request{
				Version: Version,
				ID:      1,
				Op:      OpDrain,
			},
		},
		{
			name: "deliver without message",
			request: Request{
				Version: Version,
				ID:      1,
				Op:      OpDeliver,
				Node:    &nodeTwo,
			},
		},
		{
			name: "deliver without node",
			request: Request{
				Version: Version,
				ID:      1,
				Op:      OpDeliver,
				Message: messageToTwo,
			},
		},
		{
			name: "deliver destination mismatch",
			request: Request{
				Version: Version,
				ID:      1,
				Op:      OpDeliver,
				Node:    &nodeOne,
				Message: messageToTwo,
			},
		},
		{
			name: "check with body",
			request: Request{
				Version: Version,
				ID:      1,
				Op:      OpCheck,
				Node:    &nodeOne,
			},
		},
		{
			name: "close with body",
			request: Request{
				Version: Version,
				ID:      1,
				Op:      OpClose,
				Hello:   &HelloRequest{},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateRequest(test.request); err == nil {
				t.Fatal("ValidateRequest() succeeded, want error")
			}
		})
	}
}
