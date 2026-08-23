package adapterproto

const Version uint16 = 1

type Operation string

const (
	OpHello   Operation = "hello"
	OpTick    Operation = "tick"
	OpDrain   Operation = "drain"
	OpDeliver Operation = "deliver"
	OpCheck   Operation = "check"
	OpClose   Operation = "close"
)

type Request struct {
	Version uint16        `json:"version"`
	ID      uint64        `json:"id"`
	Op      Operation     `json:"op"`
	Hello   *HelloRequest `json:"hello,omitempty"`
	Node    *uint32       `json:"node,omitempty"`
	Message *Message      `json:"message,omitempty"`
}

type Response struct {
	Version   uint16         `json:"version"`
	ID        uint64         `json:"id"`
	Op        Operation      `json:"op"`
	Error     *RemoteError   `json:"error,omitempty"`
	Hello     *HelloResponse `json:"hello,omitempty"`
	Messages  []Message      `json:"messages,omitempty"`
	Violation *Violation     `json:"violation,omitempty"`
}

type HelloRequest struct {
	Seed int64 `json:"seed"`
}

type HelloResponse struct {
	Nodes      []uint32 `json:"nodes"`
	Invariants []string `json:"invariants,omitempty"`
}

type Message struct {
	From    uint32 `json:"from"`
	To      uint32 `json:"to"`
	Kind    uint8  `json:"kind"`
	Value   uint64 `json:"value"`
	Payload []byte `json:"payload,omitempty"`
}

type Violation struct {
	Invariant string `json:"invariant"`
	Detail    string `json:"detail"`
}

type RemoteError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
