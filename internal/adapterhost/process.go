package adapterhost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

const MaxCapturedStderr = 64 << 10

type ProcessOptions struct {
	Command string
	Args    []string
	Dir     string
	Env     []string
	Stderr  io.Writer
}

type Process struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	session *Session
	stderr  *boundedBuffer

	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error
}

func StartProcess(
	ctx context.Context,
	options ProcessOptions,
	seed int64,
) (*Process, error) {
	if ctx == nil {
		return nil, errors.New("adapter host: process context is required")
	}
	if strings.TrimSpace(options.Command) == "" {
		return nil, errors.New("adapter host: process command is required")
	}

	command := exec.CommandContext(
		ctx,
		options.Command,
		options.Args...,
	)
	command.Dir = options.Dir
	if options.Env != nil {
		command.Env = append([]string(nil), options.Env...)
	}

	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("adapter host: create stdin pipe: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("adapter host: create stdout pipe: %w", err)
	}

	captured := &boundedBuffer{limit: MaxCapturedStderr}
	command.Stderr = captured
	if options.Stderr != nil {
		command.Stderr = io.MultiWriter(captured, options.Stderr)
	}

	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("adapter host: start process: %w", err)
	}

	process := &Process{
		command:   command,
		stdin:     stdin,
		stderr:    captured,
		closeDone: make(chan struct{}),
	}

	session, err := Open(stdout, stdin, seed)
	if err != nil {
		_ = stdin.Close()
		_ = command.Process.Kill()
		waitErr := command.Wait()

		return nil, processError(
			"adapter host: process handshake",
			errors.Join(err, waitErr),
			captured.String(),
		)
	}
	process.session = session

	return process, nil
}

func (p *Process) Session() *Session {
	return p.session
}

func (p *Process) Stderr() string {
	return p.stderr.String()
}

func (p *Process) Close() error {
	p.closeOnce.Do(func() {
		defer close(p.closeDone)

		sessionErr := p.session.Close()
		stdinErr := p.stdin.Close()
		waitErr := p.command.Wait()

		if waitErr != nil {
			waitErr = processError(
				"adapter host: process wait",
				waitErr,
				p.stderr.String(),
			)
		}

		p.closeErr = errors.Join(
			sessionErr,
			stdinErr,
			waitErr,
		)
	})

	<-p.closeDone
	return p.closeErr
}

func processError(
	prefix string,
	err error,
	stderr string,
) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	return fmt.Errorf(
		"%s: %w; stderr: %s",
		prefix,
		err,
		stderr,
	)
}

type boundedBuffer struct {
	mutex     sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	written := len(data)

	b.mutex.Lock()
	defer b.mutex.Unlock()

	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		if len(data) != 0 {
			b.truncated = true
		}
		return written, nil
	}
	if len(data) > remaining {
		data = data[:remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(data)
	return written, nil
}

func (b *boundedBuffer) String() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	result := b.buffer.String()
	if b.truncated {
		result += "\n[stderr truncated]"
	}
	return result
}
