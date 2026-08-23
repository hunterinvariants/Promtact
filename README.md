# Promtact

<!-- One row, gates first and facts after, each group ordered by rendered width.
     Two half-width rows left the right half of the column empty; together they
     very nearly fill it. height="22" is a 10% scale, enough to close the last
     gap without the badges looking inflated. -->
<a href="https://github.com/hunterinvariants/Promtact/actions/workflows/ci.yml"><img alt="ci" height="22" src="https://img.shields.io/github/actions/workflow/status/hunterinvariants/Promtact/ci.yml?branch=main&amp;style=flat-square&amp;labelColor=333&amp;label=ci"></a>
<a href="https://github.com/hunterinvariants/Promtact/actions/workflows/race.yml"><img alt="race" height="22" src="https://img.shields.io/github/actions/workflow/status/hunterinvariants/Promtact/race.yml?branch=main&amp;style=flat-square&amp;labelColor=333&amp;label=race"></a>
<a href="https://github.com/hunterinvariants/Promtact/actions/workflows/formal.yml"><img alt="formal" height="22" src="https://img.shields.io/github/actions/workflow/status/hunterinvariants/Promtact/formal.yml?branch=main&amp;style=flat-square&amp;labelColor=333&amp;label=formal"></a>
<a href="https://github.com/hunterinvariants/Promtact/actions/workflows/kernel.yml"><img alt="kernel" height="22" src="https://img.shields.io/github/actions/workflow/status/hunterinvariants/Promtact/kernel.yml?branch=main&amp;style=flat-square&amp;labelColor=333&amp;label=kernel"></a>
<a href="https://github.com/hunterinvariants/Promtact/actions/workflows/nightly.yml"><img alt="nightly" height="22" src="https://img.shields.io/github/actions/workflow/status/hunterinvariants/Promtact/nightly.yml?branch=main&amp;style=flat-square&amp;labelColor=333&amp;label=nightly"></a>
<a href="https://github.com/hunterinvariants/Promtact/actions/workflows/jepsen.yml"><img alt="jepsen" height="22" src="https://img.shields.io/github/actions/workflow/status/hunterinvariants/Promtact/jepsen.yml?branch=main&amp;style=flat-square&amp;labelColor=333&amp;label=jepsen"></a>
<a href="go.mod"><img alt="go" height="22" src="https://img.shields.io/github/go-mod/go-version/hunterinvariants/Promtact?style=flat-square&amp;labelColor=333&amp;color=2ca02c&amp;label=go"></a>
<a href="https://github.com/hunterinvariants/Promtact/releases"><img alt="release" height="22" src="https://img.shields.io/github/v/release/hunterinvariants/Promtact?style=flat-square&amp;labelColor=333&amp;color=2ca02c&amp;label=release"></a>
<a href="LICENSE"><img alt="license" height="22" src="https://img.shields.io/github/license/hunterinvariants/Promtact?style=flat-square&amp;labelColor=333&amp;color=2ca02c&amp;label=license"></a>
<a href="STATUS.md"><img alt="qualification" height="22" src="https://img.shields.io/badge/qualification-complete-2ca02c?style=flat-square&amp;labelColor=333"></a>

Test distributed protocols the way you test pure functions: run your own
consensus code under deterministic virtual time and injected network faults,
check invariants after every step, and reproduce any failure from its seed.

A qualified Raft implementation ships as the worked reference: durable
`io_uring` storage, a checksummed WAL, XDP/TC kernel fault injection,
Jepsen/Knossos linearizability, and a bounded TLA+ model. It is a reference
system, not a turnkey database.

The project reports only capabilities backed by executable tests, bounded model
checking, or checked-in measurements. Every claim carries its bounds in
[EVIDENCE.md](EVIDENCE.md) and [STATUS.md](STATUS.md).

## Requirements

- **Go 1.21 or newer** to build a checkout. The `toolchain` directive in
  `go.mod` fetches the release this project pins, so whatever Go your
  distribution ships is enough. The Go 1.22 on Ubuntu 24.04 switches to
  go1.25.13 by itself, with no action from you.
- **Any Go from 1.21 on** to use it as a library too, provided your `go.mod`
  names the toolchain. The Install section below does that in one line.
- **Git**, to clone.
- **A C compiler**, only for `go test ./... -race`, because `-race` requires
  cgo. Nothing else in this project does.
- **Linux**, for the `io_uring`, eBPF and raw-device commands. On other
  platforms those commands are absent rather than present and failing.
- **A Java runtime**, only for `promtact verify`. A headless JRE is enough. The
  TLA+ tools themselves are not something you install: the script downloads
  `tla2tools.jar` on first use and checks its SHA1 before running it.

```bash
sudo apt-get install -y default-jre-headless    # or your distribution's JRE
```

## Install

As a library, to drive your own protocol. `go get` works only inside a module,
so the first line is for someone starting from an empty directory:

```bash
go mod init example.com/yourproject
go mod edit -go=1.25 -toolchain=go1.25.13
go get github.com/hunterinvariants/promtact@latest
```

The middle line matters if your Go is older than 1.25. `go get` raises your `go`
directive to 1.25 and writes no toolchain to resolve it with, and from then on
every `go` command in that directory fails with `toolchain not available`.
Naming the toolchain first leaves you with a module that builds on the Go you
already have.

As a command, to run scenarios and the gates. This one needs no module and can
be run from anywhere:

```bash
go install github.com/hunterinvariants/promtact/cmd/promtact@latest
```

`@latest` asks the Go module proxy, which learns about a new tag on its own
schedule and can answer with the previous release for a while after one is
published. Name the version to get exactly it:

```bash
go install github.com/hunterinvariants/promtact/cmd/promtact@v0.3.6
```

`go install` writes the binary to `$(go env GOPATH)/bin`, which is not on
`PATH` on a fresh machine:

```bash
export PATH="$PATH:$(go env GOPATH)/bin"
```

The scenario and cluster files the commands read live in the repository, not
inside the installed binary. Clone it to run the shipped examples.

Or take a built binary, which needs no Go at all. Releases cover Linux, macOS
and Windows on amd64 and arm64, and each carries `SHA256SUMS`, an SPDX bill of
materials, and a Sigstore build attestation naming the workflow and tag that
produced it.

`scripts/install.sh` does the whole path: it resolves the latest release,
downloads the binary for your platform, refuses to install it unless the
checksum matches, verifies the attestation when `gh` 2.49+ and `jq` are
present, and writes one file to `~/.local/bin`. No root, no system packages.

```bash
git clone https://github.com/hunterinvariants/Promtact.git
cd Promtact
less scripts/install.sh
./scripts/install.sh
```

Reading it first is the point, which is why there is no one-line pipe from the
network into a shell. A script you have not read cannot prove anything to you,
and proving where a binary came from is what this one is for.

Doing it by hand instead, or checking a download you already have, is in
[SECURITY.md](SECURITY.md#verifying-a-release), including the attestation
check that needs no GitHub account.

The installer writes to `~/.local/bin`, which a fresh machine does not have on
`PATH`. It says so when it finishes; this is the same line:

```bash
export PATH="$PATH:$HOME/.local/bin"
promtact version
```

## Try it in a minute

From inside the checkout. If you have not cloned it yet:
`git clone https://github.com/hunterinvariants/Promtact.git && cd Promtact`

```bash
go test ./...
go test ./examples/paxos -v
go run ./cmd/promtact simulate -config examples/leader-partition.json
```

The full suite runs under the race detector too, which needs a C toolchain
because `-race` requires cgo. There is no cgo anywhere else in this project, so
the plain command above is enough to try it out.

The second command is the point of the project: a complete protocol that is
**not** Raft: single-decree Paxos, driven through the same engine, with its
own invariants and partition campaigns. See
[examples/paxos](examples/paxos/README.md).

Start your own from a working skeleton:

```bash
go run ./cmd/promtact new ../mysystem
(cd ../mysystem && go mod tidy && go test ./... -v)
```

The target is outside this checkout on purpose: a generated project is its own
Go module, and putting one inside the repository leaves an untracked directory
that looks like something you forgot to commit.

The parentheses run that line in a subshell, so your own shell stays in the
checkout and the commands further down this page still work. Drop them when you
are ready to move in and work on the new project.

Every command lives behind one umbrella binary. Commands needing kernel
facilities a build cannot reach are absent rather than listed and failing.

```bash
go run ./cmd/promtact help
```

## What you can build with it

Four things are pluggable. [docs/DEVELOPERS.md](docs/DEVELOPERS.md) is the guide;
[docs/API.md](docs/API.md) says which identifiers are contractual.

**Your protocol.** Implement four methods for `dst.Cluster` and two for
`dst.Wire`, and the engine supplies virtual time, a seeded schedule, message
loss and delay, and a reproducible execution trace. It is generic over your
message type, so nothing is boxed and the hot path allocates nothing.

**Your properties.** A `dst.Invariant` is evaluated after every step. A failure
comes back as a `dst.Violation` carrying the property name, the step, and the
trace hash, a coordinate you can return to, not a message you have to
reproduce by guesswork.

**Your faults.** `Split`, `Isolate`, one-way `Link` failures, and `During` for
time windows. Injectors are consulted *after* the engine draws each message's
random loss and delay, so the same seed produces the same schedule with and
without a fault, and an A/B comparison means something.

**Your storage.** Implement `wal.Device` and you inherit the checksummed record
format, sequence validation, and torn-tail recovery of `wal.Log`. Verify it
against `storagetest.RunDeviceSuite` before trusting it with consensus state.

A run can be declared as a file rather than written into a test:

```json
{"seed": "0x4A2C", "nodes": 5, "steps": 1200, "proposeEvery": 17,
 "faults": [{"type": "split", "a": [1], "b": [2,3,4,5], "start": 200, "end": 700}]}
```

## The Raft reference

- deterministic virtual time, seeded scheduling, message delay/drop, and restart;
- Raft pre-vote, elections, duplicate-safe voting, replication, commit, durable term/vote, and durable entry ACKs;
- fail-stop behavior when stable storage rejects a write;
- fixed 112-byte CRC32C WAL records, sequence validation, torn-tail recovery;
- crash/restart reconstruction exclusively from durable WAL state;
- deterministic bit-rot, misdirected-write, and phantom-prefix storage faults;
- registered-file and registered-buffer `io_uring` data path using `WRITE_FIXED`;
- CQE identity, error, and short-write validation followed by a separate `FSYNC`;
- checksummed WAL records stored in aligned 4096-byte `O_DIRECT` blocks;
- XDP ingress drop/partition and TC egress drop/corruption programs;
- namespace-safe eBPF/netem controller with mandatory cleanup;
- checksummed snapshot image format and joint-quorum calculation primitive;
- parallel seed sweeper, race tests, fuzz target, benchmarks, and CI;
- bounded TLA+ model covering election, replication, commit, snapshots, membership, and crash recovery;
- versioned CRC32C peer/client protocol, TCP multi-process service, replicated deduplication, ReadIndex reads, bounded backpressure, health/metrics, and backup/restore.

A cluster is declared once and shared by every node, which selects its own entry
by identifier. The peer list is derived from the file, so the processes cannot
disagree about who the members are.

```bash
go run ./cmd/promtactd -config examples/cluster.json -id 3
```

That runs in the foreground until you stop it, and one node of five does not
elect a leader on its own. `examples/cluster.json` puts all five on loopback, so
a whole cluster is five of those in five terminals, or one line:

```bash
go build -o /tmp/promtactd ./cmd/promtactd && go build -o /tmp/promtactctl ./cmd/promtactctl
for id in 1 2 3 4 5; do /tmp/promtactd -config examples/cluster.json -id $id & done
curl -s http://127.0.0.1:9303/healthz
```

Only the leader accepts a request. Any other node answers `status=1`, which is
`StatusNotLeader`, names the leader it knows about, and exits non-zero. That is
a redirect rather than a failure, and it is why a client tries the members until
one accepts:

```bash
for p in 9201 9202 9203 9204 9205; do
  /tmp/promtactctl -address 127.0.0.1:$p -operation put -client 1 -request 1 -key 7 -value 42 && break
done
for p in 9201 9202 9203 9204 9205; do
  /tmp/promtactctl -address 127.0.0.1:$p -operation get -client 1 -request 2 -key 7 && break
done
kill %1 %2 %3 %4 %5
```

A successful reply reads `status=0`, and for the read it carries the value back.

Killing the node that accepted the write is the part worth watching. The other
four elect a leader without being told to, and the value is still there:

```bash
kill %1                                  # node 1 was the leader above
sleep 10
for p in 9201 9202 9203 9204 9205; do
  /tmp/promtactctl -address 127.0.0.1:$p -operation get -client 2 -request 3 -key 7 && break
done
```

```text
promtactctl: dial tcp 127.0.0.1:9201: connect: connection refused
status=1 leader=4 request=3 value=0 commit=3
status=1 leader=4 request=3 value=0 commit=3
status=0 leader=4 request=3 value=42 commit=3
```

The dead node refuses the connection, two survivors redirect to the leader they
have already agreed on, and the fourth answers with the value. The commit index
moved from 2 to 3, so the new term appended and committed rather than freezing
what it inherited.

Each node keeps its state under `/var/tmp/promtact/nodeN`, which the file names
and the process creates. Remove those directories to start from nothing.

The extracted engine is verified against the simulator these gates qualified:
7,000 paired runs compare the two at every tick and require a bit-identical
execution. Evidence in
[benchmarks/sentinel-dst-engine-2026-08-17.md](benchmarks/sentinel-dst-engine-2026-08-17.md).

## Verified Linux baseline

On the `sentinel` Linux host, the following gates passed:

- Ubuntu 24.04.4 LTS, kernel `6.8.0-137-generic`, Go 1.25.13;
- `io_uring_setup`, registered buffer/file, `O_DIRECT`, `WRITE_FIXED`, CQE, `FSYNC`;
- WAL write, close, reopen, checksum validation, and bit-exact replay;
- XDP and TC verifier/JIT loading;
- isolated 25 ms TC delay and configured 10% XDP drop injection;
- namespace, veth, map, and program cleanup;
- complete `go test ./... -race -count=1` suite;
- five-process Phase 5 failover, health, metrics, backup/restore gate;
- bounded live Jepsen/Knossos workload under process and TC network faults: `valid? true`;
- bounded TLC model: 46,667,923 states generated, 6,121,927 distinct, no invariant violation.

Durable 4 KiB writes with one completed `FSYNC` per operation, through
registered `io_uring` with `O_DIRECT` and `WRITE_FIXED`:

| Target | Operations | Throughput | p50 | p99 | Max |
|---|---:|---:|---:|---:|---:|
| file on ext4 over `/dev/sda2` | 1,000 | 2,189 ops/s | 449.616 us | 543.602 us | 1.517617 ms |
| raw NVMe `/dev/nvme0n1` | 10,000 | 7,971 ops/s | 117.207 us | 196.343 us | 13.668893 ms |

These describe this machine's devices, not hardware in general. Full evidence
and commands are in [benchmarks/sentinel-2026-08-17.md](benchmarks/sentinel-2026-08-17.md).

Linux capability and integration gates:

```bash
go run ./cmd/promtact probe -entries 32
PROMTACT_URING_INTEGRATION=1 go test ./storage/uring ./storage/uringwal -count=1 -v
go run ./cmd/promtact verify -json
```

The chaos controller must be used only with its dedicated `promtact-*`
namespace and veth pair. Never attach development fault policies to a management
interface. See [bpf/README.md](bpf/README.md).

## Architecture

```text
your protocol            the Raft reference
       \                        /
        dst.Cluster / dst.Wire
                 |
   deterministic engine: virtual time, seeded
   schedule, faults, invariants, trace hash
                 |
        durable Raft core
          |           |
     CRC32C WAL   snapshots
          |
 registered io_uring + O_DIRECT

isolated netns -> XDP / TC / netem -> controlled kernel faults
```

## Repository map

- `dst/`: the protocol-agnostic engine, invariants, and fault injection;
- `dst/scenario/`: the declarative run format;
- `dst/raftcluster/`: the Raft adapter, and the equivalence campaigns;
- `examples/paxos/`: a complete protocol that is not Raft;
- `raft/`: consensus state machine, persistence boundary, quorum logic;
- `sim/`: the qualified simulator, retained as the equivalence reference;
- `storage/wal/`: portable WAL format and recovery;
- `storage/storagetest/`: conformance suite for an alternative backend;
- `storage/uring/`, `storage/uringwal/`: Linux registered I/O and its WAL adapter;
- `storage/snapshot/`: checksummed snapshot images;
- `server/`: the replicated service and its cluster file format;
- `chaos/`, `bpf/`: safe controller and kernel programs;
- `internal/cli/`: one implementation per command, shared by every binary;
- `verification/tla/`: current formal model;
- `benchmarks/`: checked-in measurement evidence.

## Documentation

- [docs/DEVELOPERS.md](docs/DEVELOPERS.md), driving your own protocol, properties, faults, and storage;
- [docs/API.md](docs/API.md), what is contractual, and what versioning will mean;
- [CONTRIBUTING.md](CONTRIBUTING.md), the evidence bar a change has to clear;
- [SPEC.md](SPEC.md), [STATUS.md](STATUS.md), [ROADMAP.md](ROADMAP.md), [EVIDENCE.md](EVIDENCE.md).

The six scoped qualification phases are complete and frozen at the documented
reference baseline. Framework work claims no phase acceptance and does not amend
that record.

## License

Apache License 2.0, see [LICENSE](LICENSE). It covers the project, including
earlier releases.

Apache rather than MIT for the express patent grant and its retaliation clause,
which matter more for a project touching `io_uring`, eBPF, and consensus than
copyleft would.

## Earlier names

This project was called `HYPERION-DST` through `v0.1.1`, then `Hyperion` for
`v0.2.x`. Both names described what it was at the time: first a deterministic
simulation testing harness, then a framework that had outgrown the suffix.

**Use `github.com/hunterinvariants/promtact` from `v0.3.0` on.** Nothing older
resolves to it. GitHub redirects the repository URL, but a Go module path is not
a redirect, imports have to be updated. Releases before `v0.3.0` stay published
under their original names and are not maintained.
