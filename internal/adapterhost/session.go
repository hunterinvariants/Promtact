package adapterhost

import (
	"errors"
	"fmt"
	"io"

	"github.com/hunterinvariants/promtact/internal/adapterproto"
)

var (
	ErrClosed      = errors.New("adapter host: session is closed")
	ErrIDExhausted = errors.New("adapter host: request ids exhausted")
)

type RemoteError struct {
	Operation adapterproto.Operation
	Code      string
	Message   string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf(
		"adapter host: remote %s failed with %s: %s",
		e.Operation,
		e.Code,
		e.Message,
	)
}

type Session struct {
	reader io.Reader
	writer io.Writer

	nextID uint64
	closed bool

	nodes        []uint32
	nodeSet      map[uint32]struct{}
	invariants   []string
	invariantSet map[string]struct{}
}

func Open(reader io.Reader, writer io.Writer, seed int64) (*Session, error) {
	if reader == nil || writer == nil {
		return nil, errors.New("adapter host: reader and writer are required")
	}

	session := &Session{
		reader:       reader,
		writer:       writer,
		nextID:       1,
		nodeSet:      make(map[uint32]struct{}),
		invariantSet: make(map[string]struct{}),
	}

	response, err := session.exchange(adapterproto.Request{
		Op:    adapterproto.OpHello,
		Hello: &adapterproto.HelloRequest{Seed: seed},
	})
	if err != nil {
		return nil, fmt.Errorf("adapter host: hello: %w", err)
	}

	session.nodes = append([]uint32(nil), response.Hello.Nodes...)
	for _, node := range session.nodes {
		session.nodeSet[node] = struct{}{}
	}

	session.invariants = append(
		[]string(nil),
		response.Hello.Invariants...,
	)
	for _, invariant := range session.invariants {
		session.invariantSet[invariant] = struct{}{}
	}

	return session, nil
}

func (s *Session) Nodes() []uint32 {
	return append([]uint32(nil), s.nodes...)
}

func (s *Session) Invariants() []string {
	return append([]string(nil), s.invariants...)
}

func (s *Session) Tick(node uint32) error {
	if err := s.requireNode(node); err != nil {
		return err
	}

	_, err := s.exchange(adapterproto.Request{
		Op:   adapterproto.OpTick,
		Node: &node,
	})
	return err
}

func (s *Session) Drain(node uint32) ([]adapterproto.Message, error) {
	if err := s.requireNode(node); err != nil {
		return nil, err
	}

	response, err := s.exchange(adapterproto.Request{
		Op:   adapterproto.OpDrain,
		Node: &node,
	})
	if err != nil {
		return nil, err
	}

	for index, message := range response.Messages {
		if err := s.requireNode(message.To); err != nil {
			return nil, fmt.Errorf(
				"adapter host: drained message %d destination: %w",
				index,
				err,
			)
		}
	}

	return response.Messages, nil
}

func (s *Session) Deliver(
	node uint32,
	message adapterproto.Message,
) error {
	if err := s.requireNode(node); err != nil {
		return err
	}
	if err := s.requireNode(message.From); err != nil {
		return fmt.Errorf("adapter host: message sender: %w", err)
	}
	if message.To != node {
		return fmt.Errorf(
			"adapter host: message destination %d does not match node %d",
			message.To,
			node,
		)
	}

	_, err := s.exchange(adapterproto.Request{
		Op:      adapterproto.OpDeliver,
		Node:    &node,
		Message: &message,
	})
	return err
}

func (s *Session) Check() (*adapterproto.Violation, error) {
	response, err := s.exchange(adapterproto.Request{
		Op: adapterproto.OpCheck,
	})
	if err != nil {
		return nil, err
	}
	if response.Violation == nil {
		return nil, nil
	}
	if _, declared := s.invariantSet[response.Violation.Invariant]; !declared {
		return nil, fmt.Errorf(
			"adapter host: adapter reported undeclared invariant %q",
			response.Violation.Invariant,
		)
	}

	violation := *response.Violation
	return &violation, nil
}

func (s *Session) Close() error {
	if s.closed {
		return nil
	}

	if _, err := s.exchange(adapterproto.Request{
		Op: adapterproto.OpClose,
	}); err != nil {
		return err
	}

	s.closed = true
	return nil
}

func (s *Session) exchange(
	request adapterproto.Request,
) (adapterproto.Response, error) {
	if s.closed {
		return adapterproto.Response{}, ErrClosed
	}
	if s.nextID == 0 {
		return adapterproto.Response{}, ErrIDExhausted
	}

	request.Version = adapterproto.Version
	request.ID = s.nextID
	s.nextID++

	if err := adapterproto.ValidateRequest(request); err != nil {
		return adapterproto.Response{}, err
	}
	if err := adapterproto.WriteFrame(s.writer, request); err != nil {
		return adapterproto.Response{}, fmt.Errorf(
			"adapter host: write %s request: %w",
			request.Op,
			err,
		)
	}

	var response adapterproto.Response
	if err := adapterproto.ReadFrame(s.reader, &response); err != nil {
		return adapterproto.Response{}, fmt.Errorf(
			"adapter host: read %s response: %w",
			request.Op,
			err,
		)
	}
	if err := adapterproto.ValidateResponseFor(request, response); err != nil {
		return adapterproto.Response{}, fmt.Errorf(
			"adapter host: validate %s response: %w",
			request.Op,
			err,
		)
	}
	if response.Error != nil {
		return adapterproto.Response{}, &RemoteError{
			Operation: request.Op,
			Code:      response.Error.Code,
			Message:   response.Error.Message,
		}
	}

	return response, nil
}

func (s *Session) requireNode(node uint32) error {
	if _, exists := s.nodeSet[node]; !exists {
		return fmt.Errorf("node %d is not configured", node)
	}
	return nil
}
