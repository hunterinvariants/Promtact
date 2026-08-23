# Process adapter protocol v1

Promtact process protocol v1 connects the Go deterministic engine to a
protocol implementation running in a separate operating-system process.
The process may be implemented in any language that can read and write the
framed JSON messages defined here.

The checked Rust token-ring adapter is the reference non-Go implementation.
This document specifies the process boundary; it does not define an
in-process SDK or native language binding.

## Transport

- The host writes requests to the child process on `stdin`.
- The child writes responses to `stdout`.
- The child reserves `stdout` exclusively for protocol frames.
- Human-readable diagnostics belong on `stderr`.
- The host sends one request at a time and waits for its response.
- Request identifiers are non-zero and increase for the life of a session.

## Frame format

Each frame consists of:

1. a four-byte unsigned big-endian payload length;
2. exactly that many bytes of UTF-8 JSON.

The maximum JSON payload is 16 MiB. A zero-length, truncated, malformed, or
oversized frame is a protocol failure. Readers must complete partial reads,
and writers must complete partial writes or report an error.

## Common envelope

Every request and response contains:

- `version`: protocol version `1`;
- `id`: the non-zero request identifier;
- `op`: one of `hello`, `tick`, `drain`, `deliver`, `check`, or `close`.

A response must repeat the request identifier and operation. The host rejects
a mismatched version, identifier, or operation.

## Operations

### `hello`

The first request contains only `hello: {"seed": SEED}` in addition to the
common envelope. The response contains `hello` with:

- `nodes`: a non-empty, stable, duplicate-free array of non-zero node IDs;
- `invariants`: an optional duplicate-free array of non-empty names.

A session has one successful `hello`. The host caches these declarations.

### `tick`

The request contains only `node`. The adapter advances that protocol node.
A successful response has no operation-specific payload.

### `drain`

The request contains only `node`. The response may contain `messages`. Every
returned message must have `from` equal to the drained node.

### `deliver`

The request contains `node` and one `message`. The node must equal the
message destination in `to`. A successful response has no operation-specific
payload.

### `check`

The request has no operation-specific fields. A successful response may
contain one `violation` with `invariant` and `detail`. The invariant name
must have been declared by the `hello` response.

### `close`

The request has no operation-specific fields. The adapter acknowledges it,
releases its resources, and terminates after writing the response.

## Protocol message

A message contains:

- `from`: source node ID;
- `to`: destination node ID;
- `kind`: protocol-defined unsigned eight-bit message kind;
- `value`: protocol-defined unsigned 64-bit trace digest value;
- `payload`: optional protocol-defined bytes, represented in JSON as Base64.

Promtact uses `from` and `to` for routing and uses `kind` and `value` when
updating the deterministic trace. The payload remains opaque to the host.
Language adapters are responsible for preserving their application message
semantics across these fields.

## Remote errors

An adapter that understood a frame but cannot perform its operation returns
`error` with:

- `code`: a stable machine-readable identifier;
- `message`: a non-empty diagnostic for the operator.

An error response contains no `hello`, `messages`, or `violation`. The host
returns the remote error to its caller without treating it as a protocol
invariant violation.

Malformed framing, invalid JSON, an unsupported envelope, premature EOF, or
a mismatched response is a session failure. The host may terminate the child
and includes bounded child `stderr` in its diagnostic.

## Session lifecycle

1. The host starts the adapter without an intermediate shell.
2. The host sends `hello` with the deterministic campaign seed.
3. The host validates and caches nodes and invariant names.
4. The host exchanges `tick`, `drain`, `deliver`, and `check` requests.
5. The host sends `close`, waits for its response, closes `stdin`, and waits
   for the child process to exit.

The host context owns the child lifetime. Cancellation or deadline expiry
terminates a child that is starting, blocked, or still running.

## Determinism

The adapter must return stable node order and deterministic results for the
same seed and the same request sequence. It must not use wall-clock time,
unordered iteration, uncontrolled randomness, or concurrent mutation that
can change protocol-visible behavior.

The host remains responsible for virtual time, scheduling, network faults,
delivery ordering, fault counters, and trace hashing.

## Versioning

Both sides send version `1` in every envelope. Implementations reject an
unknown version instead of guessing compatibility. An incompatible framing,
envelope, lifecycle, or field-semantics change requires a new protocol
version.

Adding language libraries or generated bindings does not by itself change
the wire version when their observable frames remain compliant with this
document.

## Security boundary

The process boundary is not an operating-system sandbox. The adapter runs
with the permissions and environment selected by its caller. Deployments are
responsible for executable provenance, filesystem permissions, resource
limits, and any additional process isolation they require.

The protocol requires no network service and no external callback. A local
adapter can run in an offline or restricted CI environment. Child `stderr`
is diagnostic data and is captured with a bounded host buffer.

## Conformance

A conforming adapter must demonstrate:

- Golden JSON compatibility for every operation and optional field;
- Base64 compatibility for byte payloads;
- correct framing under partial reads and writes;
- rejection of empty, truncated, malformed, and oversized frames;
- request and response correlation;
- one successful handshake with stable nodes and invariants;
- deterministic repeated campaigns;
- non-vacuous fault activation;
- a negative control that makes a declared invariant fail;
- clean shutdown and bounded failure diagnostics.

The Go implementation is in `internal/adapterproto` and
`internal/adapterhost`. The checked non-Go reference is
`examples/rust-tokenring-adapter`.
