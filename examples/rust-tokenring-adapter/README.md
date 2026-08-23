# Rust token-ring process adapter

This example is a Rust reference implementation of Promtact process protocol
v1. The Go host starts the compiled binary as a subprocess and keeps virtual
time, deterministic scheduling, fault injection, trace hashing, and campaign
control in Promtact.

This is a checked reference adapter, not a general Rust SDK or a claim of
native bindings for arbitrary Rust systems.

The wire contract is specified in
[Process adapter protocol v1](../../docs/PROCESS-ADAPTER-PROTOCOL.md).

## Boundary

The process uses:

- `stdin` for length-prefixed protocol requests;
- `stdout` for length-prefixed protocol responses only;
- `stderr` for human-readable diagnostics;
- a four-byte big-endian frame length;
- UTF-8 JSON payloads with a 16 MiB maximum frame size.

The binary receives `--nodes COUNT`. Its first request must be `hello`; the
response declares the stable node order and the `at-most-one-token`
invariant.

## Structure

- `src/protocol.rs` defines the protocol v1 JSON types and Go-compatible
  Base64 handling for byte payloads.
- `src/codec.rs` implements the bounded length-prefixed frame codec.
- `src/tokenring.rs` contains the protocol state machine without Promtact or
  transport dependencies.
- `src/adapter.rs` maps protocol requests to token-ring transitions and
  invariant results.
- `src/main.rs` owns the process boundary and reserves `stdout` for frames.
- `tests/` contains JSON, codec, state-machine, adapter, and negative-control
  tests.

The token-ring message keeps the Go-compatible trace digest in `value`. Its
full 64-bit sequence is carried separately in the payload, so delivery does
not lose the upper sequence bits.

## Build and test

The toolchain is pinned in `rust-toolchain.toml`.

    cargo test --locked --all-targets
    cargo fmt --all --check
    cargo clippy --locked --all-targets -- -D warnings
    cargo build --locked

From the Promtact repository root, build the Rust binary and set
`PROMTACT_RUST_ADAPTER` when running the cross-language tests.

    PROMTACT_RUST_ADAPTER="$PWD/examples/rust-tokenring-adapter/target/debug/promtact-rust-tokenring-adapter" \
      go test ./internal/adapterhost -run '^TestRustProcessAdapter' -count=1 -v

Without `PROMTACT_RUST_ADAPTER`, these external-process tests skip. The
dedicated CI job builds the pinned Rust binary and sets the variable
explicitly.

## Evidence

The cross-language campaign uses the same seed, maximum delay, timed network
partition, node order, and 200-step limit as the checked-in Go token-ring
consumer.

The Rust subprocess produces the exact Go reference trace:

    77a8aabf3cbd3ad3ee4b4543d36564c02a4888940d5f05c048db37eee56bada8

The test also compares the virtual step, pending-message count, fault name,
and non-zero drop count. A separate negative control delivers an additional
valid token and requires the Rust adapter to report:

    at-most-one-token: token held by nodes [1, 2]

## Scope

The example proves that a Rust protocol can participate through the process
boundary while the deterministic engine and fault model remain in Go. It
does not provide generated bindings, an in-process Rust API, C++ or Java
support, orchestration, or a hosted service.

The token-ring protocol establishes safety, determinism, fault activation,
and cross-language trace equivalence. It does not claim liveness or recovery
after message loss.
