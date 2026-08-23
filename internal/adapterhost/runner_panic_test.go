package adapterhost

import (
	"net"
	"testing"

	"github.com/hunterinvariants/promtact/dst"
)

func TestRunnerDoesNotSwallowUnexpectedPanic(t *testing.T) {
	host, adapter := net.Pipe()
	defer host.Close()

	done := servePingAdapter(adapter)

	session, err := Open(host, host, 44)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}

	runner, err := NewRunner(dst.Config{Seed: 44}, session)
	if err != nil {
		t.Fatalf("NewRunner() failed: %v", err)
	}

	runner.Inject(dst.InjectorFunc{
		Label: "unexpected-panic",
		Fn: func(
			uint64,
			uint32,
			uint32,
			uint64,
		) bool {
			panic("injector panic")
		},
	})

	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		_ = runner.StepChecked()
	}()

	if recovered != "injector panic" {
		t.Fatalf(
			"recovered panic = %#v, want %q",
			recovered,
			"injector panic",
		)
	}

	if err := runner.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	adapterResult := <-done
	if adapterResult.err != nil {
		t.Fatalf("ping adapter failed: %v", adapterResult.err)
	}
}
