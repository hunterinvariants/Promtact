package adapterproto

import "testing"

func TestValidateResponseForAcceptsMatchingExchange(t *testing.T) {
	node := uint32(2)

	request := Request{
		Version: Version,
		ID:      41,
		Op:      OpDrain,
		Node:    &node,
	}
	response := Response{
		Version: Version,
		ID:      41,
		Op:      OpDrain,
		Messages: []Message{
			{From: 2, To: 1, Kind: 3, Value: 5},
			{From: 2, To: 3, Kind: 4, Value: 8},
		},
	}

	if err := ValidateResponseFor(request, response); err != nil {
		t.Fatalf("ValidateResponseFor() failed: %v", err)
	}
}

func TestValidateResponseForAcceptsMatchingRemoteError(t *testing.T) {
	node := uint32(9)

	request := Request{
		Version: Version,
		ID:      42,
		Op:      OpTick,
		Node:    &node,
	}
	response := Response{
		Version: Version,
		ID:      42,
		Op:      OpTick,
		Error: &RemoteError{
			Code:    "invalid-node",
			Message: "node 9 is not configured",
		},
	}

	if err := ValidateResponseFor(request, response); err != nil {
		t.Fatalf("ValidateResponseFor() failed: %v", err)
	}
}

func TestValidateResponseForRejectsMismatchedExchange(t *testing.T) {
	nodeOne := uint32(1)

	validRequest := Request{
		Version: Version,
		ID:      50,
		Op:      OpDrain,
		Node:    &nodeOne,
	}
	validResponse := Response{
		Version: Version,
		ID:      50,
		Op:      OpDrain,
		Messages: []Message{
			{From: 1, To: 2},
		},
	}

	tests := []struct {
		name     string
		request  Request
		response Response
	}{
		{
			name: "invalid request",
			request: Request{
				Version: Version,
				ID:      50,
				Op:      OpDrain,
			},
			response: validResponse,
		},
		{
			name:    "invalid response",
			request: validRequest,
			response: Response{
				Version: Version,
				ID:      50,
				Op:      OpHello,
			},
		},
		{
			name:    "response id mismatch",
			request: validRequest,
			response: Response{
				Version:  Version,
				ID:       51,
				Op:       OpDrain,
				Messages: validResponse.Messages,
			},
		},
		{
			name:    "response operation mismatch",
			request: validRequest,
			response: Response{
				Version: Version,
				ID:      50,
				Op:      OpTick,
			},
		},
		{
			name:    "drained sender mismatch",
			request: validRequest,
			response: Response{
				Version: Version,
				ID:      50,
				Op:      OpDrain,
				Messages: []Message{
					{From: 2, To: 1},
				},
			},
		},
		{
			name: "deliver destination mismatch in request",
			request: Request{
				Version: Version,
				ID:      50,
				Op:      OpDeliver,
				Node:    &nodeOne,
				Message: &Message{
					From: 2,
					To:   2,
				},
			},
			response: Response{
				Version: Version,
				ID:      50,
				Op:      OpDeliver,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateResponseFor(test.request, test.response); err == nil {
				t.Fatal("ValidateResponseFor() succeeded, want error")
			}
		})
	}
}
