package simulation

import (
	"fmt"

	"github.com/hunterinvariants/promtact/dst"
	"github.com/hunterinvariants/promtact/examples/tokenring-consumer/tokenring"
)

// cluster adapts the protocol without adding protocol state of its own.
type cluster struct {
	ring  *tokenring.Ring
	nodes []uint32
}

func newCluster(count int) (*cluster, error) {
	ring, err := tokenring.New(count)
	if err != nil {
		return nil, err
	}

	return &cluster{
		ring:  ring,
		nodes: ring.NodeIDs(),
	}, nil
}

func (c *cluster) Nodes() []uint32 {
	return c.nodes
}

func (c *cluster) Tick(node uint32) {
	c.ring.Advance(node)
}

func (c *cluster) Deliver(node uint32, message tokenring.Message) {
	c.ring.Receive(node, message)
}

func (c *cluster) Drain(node uint32, destination []tokenring.Message) []tokenring.Message {
	return append(destination, c.ring.TakeOutbound(node)...)
}

// wire exposes the protocol-visible message fields to the engine.
type wire struct{}

func (wire) Route(message tokenring.Message) (uint32, uint32) {
	return message.From, message.To
}

func (wire) Digest(message tokenring.Message) (uint8, uint64) {
	const tokenMessage uint8 = 1

	value := message.Sequence<<32 |
		uint64(message.From)<<16 |
		uint64(message.To)

	return tokenMessage, value
}

func atMostOneToken(ring *tokenring.Ring) dst.Invariant {
	return dst.InvariantFunc{
		Label: "at-most-one-token",
		Fn: func() error {
			holders := ring.HolderIDs()
			if len(holders) > 1 {
				return fmt.Errorf("token held by nodes %v", holders)
			}

			return nil
		},
	}
}

var (
	_ dst.Cluster[tokenring.Message] = (*cluster)(nil)
	_ dst.Wire[tokenring.Message]    = wire{}
)
