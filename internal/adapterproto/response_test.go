package adapterproto

import "testing"

func TestValidateResponseAcceptsEveryOperation(t *testing.T) {
	tests := []struct {
		name     string
		response Response
	}{
		{
			name: "hello",
			response: Response{
				Version: Version,
				ID:      1,
				Op:      OpHello,
				Hello: &HelloResponse{
					Nodes:      []uint32{0, 2, 7},
					Invariants: []string{"at-most-one-token", "term-monotonic"},
				},
			},
		},
		{
			name: "tick",
			response: Response{
				Version: Version,
				ID:      2,
				Op:      OpTick,
			},
		},
		{
			name: "drain",
			response: Response{
				Version: Version,
				ID:      3,
				Op:      OpDrain,
				Messages: []Message{
					{From: 2, To: 7, Kind: 1, Value: 9},
				},
			},
		},
		{
			name: "deliver",
			response: Response{
				Version: Version,
				ID:      4,
				Op:      OpDeliver,
			},
		},
		{
			name: "check without violation",
			response: Response{
				Version: Version,
				ID:      5,
				Op:      OpCheck,
			},
		},
		{
			name: "check with violation",
			response: Response{
				Version: Version,
				ID:      6,
				Op:      OpCheck,
				Violation: &Violation{
					Invariant: "at-most-one-token",
					Detail:    "token held by nodes [2 3]",
				},
			},
		},
		{
			name: "close",
			response: Response{
				Version: Version,
				ID:      7,
				Op:      OpClose,
			},
		},
		{
			name: "remote error",
			response: Response{
				Version: Version,
				ID:      8,
				Op:      OpTick,
				Error: &RemoteError{
					Code:    "invalid-node",
					Message: "node 9 is not configured",
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateResponse(test.response); err != nil {
				t.Fatalf("ValidateResponse() failed: %v", err)
			}
		})
	}
}

func TestValidateResponseRejectsInvalidShapes(t *testing.T) {
	tests := []struct {
		name     string
		response Response
	}{
		{
			name: "unsupported version",
			response: Response{
				Version: Version + 1,
				ID:      1,
				Op:      OpTick,
			},
		},
		{
			name: "zero request id",
			response: Response{
				Version: Version,
				Op:      OpTick,
			},
		},
		{
			name: "unknown operation",
			response: Response{
				Version: Version,
				ID:      1,
				Op:      Operation("unknown"),
				Error: &RemoteError{
					Code:    "failure",
					Message: "failed",
				},
			},
		},
		{
			name: "remote error without code",
			response: Response{
				Version: Version,
				ID:      1,
				Op:      OpTick,
				Error: &RemoteError{
					Message: "failed",
				},
			},
		},
		{
			name: "remote error without message",
			response: Response{
				Version: Version,
				ID:      1,
				Op:      OpTick,
				Error: &RemoteError{
					Code: "failure",
				},
			},
		},
		{
			name: "remote error with success payload",
			response: Response{
				Version: Version,
				ID:      1,
				Op:      OpDrain,
				Error: &RemoteError{
					Code:    "failure",
					Message: "failed",
				},
				Messages: []Message{{From: 1, To: 2}},
			},
		},
		{
			name: "hello without body",
			response: Response{
				Version: Version,
				ID:      1,
				Op:      OpHello,
			},
		},
		{
			name: "hello without nodes",
			response: Response{
				Version: Version,
				ID:      1,
				Op:      OpHello,
				Hello:   &HelloResponse{},
			},
		},
		{
			name: "hello with duplicate node",
			response: Response{
				Version: Version,
				ID:      1,
				Op:      OpHello,
				Hello: &HelloResponse{
					Nodes: []uint32{1, 2, 1},
				},
			},
		},
		{
			name: "hello with empty invariant",
			response: Response{
				Version: Version,
				ID:      1,
				Op:      OpHello,
				Hello: &HelloResponse{
					Nodes:      []uint32{1},
					Invariants: []string{""},
				},
			},
		},
		{
			name: "hello with duplicate invariant",
			response: Response{
				Version: Version,
				ID:      1,
				Op:      OpHello,
				Hello: &HelloResponse{
					Nodes:      []uint32{1},
					Invariants: []string{"safety", "safety"},
				},
			},
		},
		{
			name: "hello with messages",
			response: Response{
				Version: Version,
				ID:      1,
				Op:      OpHello,
				Hello: &HelloResponse{
					Nodes: []uint32{1},
				},
				Messages: []Message{{From: 1, To: 2}},
			},
		},
		{
			name: "tick with messages",
			response: Response{
				Version:  Version,
				ID:       1,
				Op:       OpTick,
				Messages: []Message{{From: 1, To: 2}},
			},
		},
		{
			name: "deliver with violation",
			response: Response{
				Version: Version,
				ID:      1,
				Op:      OpDeliver,
				Violation: &Violation{
					Invariant: "safety",
					Detail:    "failed",
				},
			},
		},
		{
			name: "drain with hello",
			response: Response{
				Version: Version,
				ID:      1,
				Op:      OpDrain,
				Hello: &HelloResponse{
					Nodes: []uint32{1},
				},
			},
		},
		{
			name: "drain with violation",
			response: Response{
				Version: Version,
				ID:      1,
				Op:      OpDrain,
				Violation: &Violation{
					Invariant: "safety",
					Detail:    "failed",
				},
			},
		},
		{
			name: "check with messages",
			response: Response{
				Version:  Version,
				ID:       1,
				Op:       OpCheck,
				Messages: []Message{{From: 1, To: 2}},
			},
		},
		{
			name: "check with empty invariant name",
			response: Response{
				Version: Version,
				ID:      1,
				Op:      OpCheck,
				Violation: &Violation{
					Detail: "failed",
				},
			},
		},
		{
			name: "check with empty detail",
			response: Response{
				Version: Version,
				ID:      1,
				Op:      OpCheck,
				Violation: &Violation{
					Invariant: "safety",
				},
			},
		},
		{
			name: "close with hello",
			response: Response{
				Version: Version,
				ID:      1,
				Op:      OpClose,
				Hello: &HelloResponse{
					Nodes: []uint32{1},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateResponse(test.response); err == nil {
				t.Fatal("ValidateResponse() succeeded, want error")
			}
		})
	}
}
