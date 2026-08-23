package tokenring

import "fmt"

// Message transfers ownership of the token between two nodes.
type Message struct {
	From     uint32
	To       uint32
	Sequence uint64
}

type node struct {
	holding  bool
	sequence uint64
	outbound []Message
}

// Ring is a deterministic token-ring protocol.
//
// The first node owns the initial token. A node relinquishes ownership before
// sending the token to its successor. Messages with stale sequence numbers are
// ignored.
type Ring struct {
	ids    []uint32
	nodes  map[uint32]*node
	passes uint64
}

// New creates a ring containing node identifiers 1 through count.
func New(count int) (*Ring, error) {
	if count < 2 {
		return nil, fmt.Errorf("tokenring: node count must be at least 2")
	}

	ring := &Ring{
		ids:   make([]uint32, count),
		nodes: make(map[uint32]*node, count),
	}

	for index := range count {
		id := uint32(index + 1)
		ring.ids[index] = id
		ring.nodes[id] = &node{}
	}

	ring.nodes[ring.ids[0]].holding = true
	return ring, nil
}

// NodeIDs returns the node identifiers in stable ring order.
func (r *Ring) NodeIDs() []uint32 {
	return append([]uint32(nil), r.ids...)
}

// Advance lets a node holding the token pass it to its successor.
func (r *Ring) Advance(id uint32) {
	current, ok := r.nodes[id]
	if !ok || !current.holding {
		return
	}

	current.holding = false
	current.sequence++

	message := Message{
		From:     id,
		To:       r.successor(id),
		Sequence: current.sequence,
	}

	current.outbound = append(current.outbound, message)
	r.passes++
}

// Receive delivers a token message to its destination.
//
// Unknown nodes, incorrectly routed messages, and stale token sequences are
// ignored.
func (r *Ring) Receive(id uint32, message Message) {
	current, ok := r.nodes[id]
	if !ok || message.To != id {
		return
	}

	if _, ok := r.nodes[message.From]; !ok {
		return
	}

	if message.Sequence <= current.sequence {
		return
	}

	current.sequence = message.Sequence
	current.holding = true
}

// TakeOutbound returns and clears a node's pending messages.
func (r *Ring) TakeOutbound(id uint32) []Message {
	current, ok := r.nodes[id]
	if !ok || len(current.outbound) == 0 {
		return nil
	}

	outbound := current.outbound
	current.outbound = nil
	return outbound
}

// HolderIDs returns the current token holders in stable node order.
func (r *Ring) HolderIDs() []uint32 {
	holders := make([]uint32, 0, 1)

	for _, id := range r.ids {
		if r.nodes[id].holding {
			holders = append(holders, id)
		}
	}

	return holders
}

// Passes reports how many token transfers have been initiated.
func (r *Ring) Passes() uint64 {
	return r.passes
}

func (r *Ring) successor(id uint32) uint32 {
	for index, candidate := range r.ids {
		if candidate == id {
			return r.ids[(index+1)%len(r.ids)]
		}
	}

	return 0
}
