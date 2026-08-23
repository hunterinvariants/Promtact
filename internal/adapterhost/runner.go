package adapterhost

import (
	"errors"
	"fmt"

	"github.com/hunterinvariants/promtact/dst"
	"github.com/hunterinvariants/promtact/internal/adapterproto"
)

type RunError struct {
	Step  uint64
	Trace string
	Op    string
	Err   error
}

func (e *RunError) Error() string {
	return fmt.Sprintf(
		"adapter host: %s failed at step %d (trace %s): %v",
		e.Op,
		e.Step,
		e.Trace,
		e.Err,
	)
}

func (e *RunError) Unwrap() error {
	return e.Err
}

type Runner struct {
	session *Session
	engine  *dst.Engine[adapterproto.Message]
}

func NewRunner(
	config dst.Config,
	session *Session,
) (*Runner, error) {
	if session == nil {
		return nil, errors.New("adapter host: session is required")
	}
	if session.closed {
		return nil, ErrClosed
	}

	bridge := &clusterBridge{
		session: session,
		nodes:   session.Nodes(),
	}
	return &Runner{
		session: session,
		engine:  dst.New(config, bridge, bridge),
	}, nil
}

func (r *Runner) StepChecked() (result error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}

		failure, ok := recovered.(callbackFailure)
		if !ok {
			panic(recovered)
		}
		result = r.runError(failure.operation, failure.err)
	}()

	r.engine.Step()

	violation, err := r.session.Check()
	if err != nil {
		return r.runError("check invariants", err)
	}
	if violation != nil {
		return &dst.Violation{
			Invariant: violation.Invariant,
			Step:      r.engine.Now,
			Trace:     r.engine.TraceHash(),
			Err:       errors.New(violation.Detail),
		}
	}
	return nil
}

func (r *Runner) RunChecked(steps uint64) error {
	for range steps {
		if err := r.StepChecked(); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) Inject(injectors ...dst.Injector) {
	r.engine.Inject(injectors...)
}

func (r *Runner) InjectedDrops() map[string]int {
	return r.engine.InjectedDrops()
}

func (r *Runner) Pending() int {
	return r.engine.Pending()
}

func (r *Runner) Now() uint64 {
	return r.engine.Now
}

func (r *Runner) TraceHash() string {
	return r.engine.TraceHash()
}

func (r *Runner) Close() error {
	return r.session.Close()
}

func (r *Runner) runError(operation string, err error) *RunError {
	return &RunError{
		Step:  r.engine.Now,
		Trace: r.engine.TraceHash(),
		Op:    operation,
		Err:   err,
	}
}

type clusterBridge struct {
	session *Session
	nodes   []uint32
}

func (c *clusterBridge) Nodes() []uint32 {
	return c.nodes
}

func (c *clusterBridge) Tick(node uint32) {
	if err := c.session.Tick(node); err != nil {
		panic(callbackFailure{
			operation: fmt.Sprintf("tick node %d", node),
			err:       err,
		})
	}
}

func (c *clusterBridge) Deliver(
	node uint32,
	message adapterproto.Message,
) {
	if err := c.session.Deliver(node, message); err != nil {
		panic(callbackFailure{
			operation: fmt.Sprintf("deliver to node %d", node),
			err:       err,
		})
	}
}

func (c *clusterBridge) Drain(
	node uint32,
	destination []adapterproto.Message,
) []adapterproto.Message {
	messages, err := c.session.Drain(node)
	if err != nil {
		panic(callbackFailure{
			operation: fmt.Sprintf("drain node %d", node),
			err:       err,
		})
	}
	return append(destination, messages...)
}

func (*clusterBridge) Route(
	message adapterproto.Message,
) (uint32, uint32) {
	return message.From, message.To
}

func (*clusterBridge) Digest(
	message adapterproto.Message,
) (uint8, uint64) {
	return message.Kind, message.Value
}

type callbackFailure struct {
	operation string
	err       error
}
