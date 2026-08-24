# Token-ring downstream example

This nested module demonstrates using the public `dst` package from a
released Promtact version in an independently defined protocol.

Its `go.mod` requires Promtact v0.3.6 and contains no local `replace`
directive. Tests therefore exercise the API available to downstream users,
rather than the Promtact source in the surrounding repository.

## Structure

- `tokenring/` contains the protocol and its unit tests. It does not import
  Promtact.
- `simulation/adapter.go` implements `dst.Cluster`, `dst.Wire`, and the
  at-most-one-token safety invariant.
- `simulation/adoption_test.go` checks deterministic replay and an active,
  time-bounded network partition.
- `simulation/diagnostics_test.go` uses a deliberately faulty test adapter
  to verify invariant diagnostics.

## Run

The module declares Go 1.25.0 as its minimum version and pins `go1.25.13`
as the qualified toolchain.

    go test ./...
    go vet ./...

Repeat the deterministic campaigns with:

    go test -count=20 ./...

## What the tests demonstrate

Running the same seeded campaign twice produces the same protocol result
and trace hash. The partition campaign also requires a non-zero dropped
message count, so it cannot pass without activating the configured fault.

The diagnostic test deliberately violates the at-most-one-token invariant.
It requires the resulting error to identify the invariant, virtual step,
trace hash, and conflicting protocol state. Repeating the faulty run must
produce the complete diagnostic again.

The module graph contains only this consumer and Promtact. The simulation
compiles only the public `github.com/hunterinvariants/promtact/dst` package.

## Scope

The example establishes deterministic execution, active fault injection,
and the at-most-one-token safety property. It does not claim liveness or
recovery after message loss: a node relinquishes ownership before sending
the token, and the protocol does not retransmit a lost token.

The duplicate transfer in `simulation/diagnostics_test.go` is an intentional
negative control used only to prove that the invariant can fail and that its
diagnostic is reproducible.
