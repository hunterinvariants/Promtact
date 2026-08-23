# Building on Promtact

Promtact is a reference consensus implementation, but its testing machinery
is not specific to Raft. This document is for using that machinery on your own
system.

Three things are pluggable: the protocol under test, the properties it must
satisfy, and the faults it must survive. A fourth, the durable storage backend,
comes with a conformance suite rather than an abstraction to implement.

[docs/API.md](API.md) says which identifiers are contractual and which are
reference implementation you should not build on. Nothing is version-tagged
yet, so read that before depending on any of it.

## Driving your own protocol

`dst.Engine` owns virtual time, seeded message scheduling, loss and delay, and a
reproducible execution trace. It owns no protocol state. You supply two small
interfaces and keep everything else.

```go
type Cluster[M any] interface {
    Nodes() []uint32
    Tick(node uint32)
    Deliver(node uint32, msg M)
    Drain(node uint32, dst []M) []M
}

type Wire[M any] interface {
    Route(msg M) (from, to uint32)
    Digest(msg M) (kind uint8, value uint64)
}
```

`M` is your message type. The engine is generic over it, so messages are never
boxed and the hot path allocates nothing.

A minimal implementation:

```go
type cluster struct {
    ids   []uint32
    nodes map[uint32]*myNode
}

func (c *cluster) Nodes() []uint32                  { return c.ids }
func (c *cluster) Tick(id uint32)                   { c.nodes[id].tick() }
func (c *cluster) Deliver(id uint32, m myMessage)   { c.nodes[id].receive(m) }
func (c *cluster) Drain(id uint32, dst []myMessage) []myMessage {
    return c.nodes[id].drainOutbound(dst)
}
func (c *cluster) Route(m myMessage) (uint32, uint32) { return m.From, m.To }
func (c *cluster) Digest(m myMessage) (uint8, uint64) { return m.Kind, m.Term }

const seed int64 = 1
engine := dst.New[myMessage](dst.Config{Seed: seed, DropPermille: 50, MaxDelay: 5}, c, c)
engine.Run(10_000)
```

Two worked examples exist. `examples/paxos` is a complete, self-contained
protocol that is deliberately **not** Raft: single-decree Paxos, with its own
invariants, its own scenario file, and campaigns that refuse to pass without
exercising the mechanism that makes it safe. Start there.
`dst/raftcluster` is the other: it is what this repository's own Raft core
looks like behind these interfaces, and it is verified to drive the core
identically to the simulator that was qualified before the extraction.

### Validate as a downstream module

The checked-in
[token-ring consumer](../examples/tokenring-consumer/README.md) is the
reference downstream validation. It is a separate Go module pinned to
Promtact v0.3.6 and has no local `replace` directive. Its dedicated CI job
therefore checks the API that downstream users receive rather than the
current repository checkout.

When validating another adapter, follow the same pattern: run the same seed
twice, assert that every configured fault activates, and use a negative
control to prove that each safety invariant can fail. Inspect
`go list -m all` and `go list -deps`, then repeat the suite with fresh
module and build caches.

### The determinism contract

Every method above must be deterministic. The usual way to break this is
iterating a Go map: `Nodes()` must return a stable order, and any internal loop
whose order affects behavior must not come from map iteration.

`TraceHash()` is a running digest over every delivery. Two runs with the same
seed, the same cluster, and the same caller actions must report the same hash at
every step. When they diverge, the first differing step localizes the
nondeterminism.

## Checking properties

An `Invariant` is a named property the engine evaluates after each step.

```go
type Invariant interface {
    Name() string
    Check() error
}
```

Register with `Watch`, then drive with `StepChecked` or `RunChecked` instead of
`Step` and `Run`:

```go
engine.Watch(dst.InvariantFunc{
    Label: "one leader per term",
    Fn:    c.checkElectionSafety,
})
if err := engine.RunChecked(10_000); err != nil {
    var v *dst.Violation
    errors.As(err, &v)
    log.Fatalf("seed %d: %s broke at step %d, trace %s", seed, v.Invariant, v.Step, v.Trace)
}
```

A `Violation` carries the property name, the step, and the trace hash. The
runner must retain the seed and repeat the same caller actions; the trace is a
coordinate within that run, not a complete replay recipe.

`Step` and `Run` never evaluate invariants, so adding them to an existing loop
changes nothing until you opt in.

`raftcluster.SafetyInvariants()` packages the Raft properties, election safety,
index sanity, committed-prefix agreement, and is a useful shape to copy.

### Make your invariants fail on purpose

An invariant that never fires is indistinguishable from one that is never
evaluated. Write a mutation test: corrupt exactly the state the property
protects and require it to report the violation. The three in
`dst/raftcluster/invariants_test.go` do this.

## Injecting faults

An `Injector` decides whether a message survives.

```go
engine.Inject(dst.During(200, 700, dst.Split([]uint32{1}, []uint32{2, 3, 4, 5})))
engine.Inject(dst.Link(1, 2))   // one-way failure
engine.Inject(dst.Isolate(3))   // unreachable but running
```

Injectors are consulted *after* the engine draws a message's random loss and
delay. This is deliberate: the seeded stream is identical with and without a
fault, so a run with a partition and a run without one on the same seed differ
only because of the partition. That makes A/B comparison meaningful rather than
confounded by schedule drift.

`InjectedDrops()` reports how many messages each fault discarded. Assert on it.
A campaign whose partition dropped nothing proves nothing, and the count is the
only way to tell that apart from a campaign that survived one.

## Declaring runs as files

A scenario file describes a reproducible run, so a campaign can be reviewed and
replayed by someone who was not there when it was written.

```json
{
  "name": "leader partition and heal",
  "seed": "0x4A2C",
  "nodes": 5,
  "steps": 1200,
  "dropPermille": 20,
  "maxDelay": 4,
  "proposeEvery": 17,
  "faults": [
    {"type": "split", "a": [1], "b": [2, 3, 4, 5], "start": 200, "end": 700}
  ]
}
```

```bash
go run ./cmd/promtact simulate -config examples/leader-partition.json
```

```
scenario="leader partition and heal" seed=0x4A2C nodes=5 steps=1200 leader=2 proposed=70 max_commit=42 trace=0ec8...
fault="split[1]|[2 3 4 5]@[200,700)" dropped=945
```

Parsing is strict: an unknown field, an unknown fault type, a node outside the
cluster, a node on both sides of a split, or a backwards window is an error. A
scenario that quietly did less than it claimed would produce evidence for a
campaign that never ran.

The format describes the engine and its faults, not Raft. Another protocol
reuses `dst/scenario` and supplies its own runner; `internal/cli/simulate.go` is
about sixty lines and shows what that runner does.

## Plugging in storage

There is no storage abstraction to implement beyond `wal.Device`:

```go
type Device interface {
    Append([]byte) error
    Sync() error
    DurableBytes() []byte
    TruncateDurable(int) error
}
```

Implement those four and you get the checksummed record format, sequence
validation, and torn-tail recovery of `wal.Log` on top. The consensus
persistence boundary above it is `raft.StableStore` and `raft.SnapshotStore`,
implemented by `storage/raftwal`.

Verify a new backend before trusting it:

```go
func TestMyDeviceConformance(t *testing.T) {
    storagetest.RunDeviceSuite(t, func(t *testing.T) wal.Device {
        return newMyDevice(t)
    })
}
```

The suite covers the properties `wal.Log` relies on, including the two that are
easy to get wrong: `DurableBytes` must return a copy, and `Append` after
`TruncateDurable` must continue from the truncated end rather than leaving a
hole.

Note that `storage.Entry` is not `raft.Entry`. The consensus core keeps entries
in a slice where position carries the index, so `raft.Entry` has no index field;
a durable record has no position and must carry one. They are converted at the
`raftwal` seam.

## What this does not do

- **The engine is single-threaded.** It is not safe for concurrent use.
  Parallelism belongs between runs, not inside one.
- **Invariants are safety properties.** No liveness property is checked, and a
  run that makes no progress violates nothing. If progress matters to you,
  assert on it explicitly after the run.
- **Trace hashes are comparable within a process, not across builds.** They are
  a tool for comparing two runs on one machine, not a recorded fingerprint.
- **The in-flight queue is a binary heap** on `(at, seq)`. Delivery order is
  identical to the linear scan the qualified simulator still uses, which is
  what the equivalence campaigns check. Below roughly ten nodes at a small
  delay bound the two cost the same; the heap matters at wide topologies with
  high delay bounds, where the scan is seven times slower.
- **Kernel-level fault injection is separate.** `chaos` and `bpf/` drive real
  XDP/TC programs and keep hard safety guards: a dedicated `promtact-*`
  namespace, validated CIDRs, and bounded delay and loss. Those guards are not
  extension points, and the deterministic `Injector` above has nothing to do
  with them.
