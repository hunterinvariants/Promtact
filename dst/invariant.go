package dst

import "fmt"

// Invariant is a property that must hold after every step of a run.
//
// An implementation closes over the concrete cluster it inspects; the engine
// deliberately does not hand protocol state to it, because the engine has none.
// Check must be free of side effects and must not depend on anything outside
// the cluster, or a reported violation will not reproduce.
type Invariant interface {
	// Name identifies the property in a violation report.
	Name() string
	// Check returns a non-nil error describing the violation, or nil.
	Check() error
}

// InvariantFunc adapts a plain function to Invariant.
type InvariantFunc struct {
	Label string
	Fn    func() error
}

func (f InvariantFunc) Name() string { return f.Label }

func (f InvariantFunc) Check() error { return f.Fn() }

// Violation reports a failed invariant at an exact point in a run.
//
// It records the invariant, step, and trace hash. Reproducing the run also
// requires the caller to retain the seed and repeat the same caller actions.
type Violation struct {
	// Invariant is the name of the property that failed.
	Invariant string
	// Step is the virtual time at which the violation was detected.
	Step uint64
	// Trace is the engine's trace hash at the point of detection.
	Trace string
	// Err is the error the invariant returned.
	Err error
}

func (v *Violation) Error() string {
	return fmt.Sprintf("dst: invariant %q violated at step %d (trace %s): %v",
		v.Invariant, v.Step, v.Trace, v.Err)
}

func (v *Violation) Unwrap() error { return v.Err }
