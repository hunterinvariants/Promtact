# Ubuntu VM qualification, 2026-08-24

This qualification started from a fresh Ubuntu virtual machine and exercised
the public installation paths, released artifacts, deterministic framework,
cross-language adapter, formal model, Linux storage paths, multi-process
cluster, backup and restore, and kernel fault controls.

The run used isolated homes and caches where the boundary being tested required
them. Kernel fault tests used only the dedicated `promtact-chaos` namespace,
the `promtact-*` veth pair, and the reserved `192.0.2.0/30` test network. The
management interface `ens33` and its `192.168.183.0/24` network were outside
every fault plan.

## Environment

| Item | Value |
|---|---|
| Host | `senti` virtual machine |
| Operating system | Ubuntu 24.04.4 LTS |
| Kernel | `6.8.0-138-generic` |
| Architecture | x86_64 |
| Logical CPUs | 2 |
| Memory | 1.9 GiB |
| Swap | 2.0 GiB |
| Root filesystem | 19 GiB ext4 |
| Management interface | `ens33`, `192.168.183.138/24` |
| Final checkout toolchain | `go1.25.13 linux/amd64` |

At the final checkpoint, 432 MiB of memory was in use, 1.5 GiB was available,
and the root filesystem used 55 percent of its capacity.

## Source and release identities

The qualification followed public `main` as fixes discovered by the run were
reviewed, tested, and published:

| Purpose | Commit or release |
|---|---|
| Broad checkout qualification | `31dc141450e2220e2b3611a178771b47e2964f3b` |
| Atomic shared BPF policy | `d4a8b25a75d3f7b46803f35e666607ee905683ad` |
| Explicit ownership refusals | `c45aa75afebd409c15cb4b61e78dd7211c6eef1d` |
| Corrected installation prerequisites | `f8f0bf22325cbaba69771d0d072e06a87d7db744` |
| Released command and library | Promtact v0.3.6 |

The v0.3.6 Linux amd64 release reported revision
`f81b58a7218b58360004974d58ee8a4a9b88804c`, build time
`2026-08-17T21:43:36Z`, and Go `1.25.13`.

Every source checkout was clean when its gates completed. The final checkout
was `f8f0bf22325cbaba69771d0d072e06a87d7db744`.

## Raw evidence boundary

The VM retained 67 run files under
`/var/tmp/promtact-vm-challenge/runs`, occupying 328 KiB. Their sorted SHA-256
manifest is:

    /var/tmp/promtact-vm-challenge/evidence-manifest.sha256
    bc535d138a5065d0733153287d640118eefba78835056f558f7a0e16f95f799e

Key final records were:

| Record | SHA-256 |
|---|---|
| Ownership refusals | `8c413aefeedfc3187da5a3cf547d32ad5174b407df3624a8dea237641a03826a` |
| Clean-cache toolchain validation | `144bd941fccfe1027a52a7a8c3f323ba39d1a05d0614bda46dda09bbbd6c1844` |
| Ubuntu BPF prerequisite validation | `1bebc661312bdccd5692c98f07c802a9c6682e0288c30b20d268fd003dd8ed1d` |
| Final VM state | `89e487a3bc553c27f820e50fe570ed5c2d9f3df4129dd7379235b04af296d414` |

The raw files remain on the qualification VM. This checked-in report records
the commands, observed results, identities, and claim boundaries needed to
interpret them.

## Public installation paths

The README library installation was executed in an isolated empty home. The
resulting module graph contained the new consumer and Promtact v0.3.6, with no
local `replace` directive.

The checkout declared Go 1.25 and pinned `go1.25.13`. Starting with the Ubuntu
Go command caused the Go toolchain mechanism to download and activate
`go1.25.13`.

The nested token-ring consumer initially declared only Go 1.25.0. From an empty
module cache, that declaration selected `go1.25.0`. The qualification finding
led to commit `f8f0bf2`, which added `toolchain go1.25.13`. A new isolated home,
module cache, and build cache then selected `go1.25.13` on the first command.
Module verification, tests, vet, and twenty repeated campaigns passed in 37
seconds.

Installing the v0.3.6 command with `go install package@version` from an empty
home selected a newer compatible Go compiler, `go1.26.7`, while the installed
Promtact version remained v0.3.6. The public documentation now distinguishes
this command-install behavior from reproducing the qualified release compiler.

The released binary remains the path for users who need the published compiler
identity without building a checkout.

## Generator and downstream adoption

The documented generator command created a new `mysystem` module containing
the protocol, cluster adapter, scenario, tests, README, and module declaration.
The generated `protocol_test.go` and the rest of the project were then used
directly for the documented tidy and test commands.

The documented commands then completed:

    go mod tidy
    go test ./... -v

The generated tests passed the single-proposer case, required the engine to
detect the deliberately unsafe two-proposer case, loaded the scenario file,
and reproduced the same run.

The released token-ring consumer used Promtact v0.3.6 from the public module
cache, imported only the public `dst` surface, activated its timed partition,
and passed twenty repeated campaigns plus vet.

## Release integrity and provenance

The v0.3.6 Linux amd64 release artifact had SHA-256:

    2d28e7e93e9eebe6b6e7bef82cecbf6fd408c6cd4c8c8e3d4e632b0a5c9d49d2

The reviewed installer ran without root in an isolated user home. It verified
the checksum and installed only:

    .local/bin/promtact

With GitHub CLI 2.93.0 available, the installer also verified that the artifact
was built by:

    hunterinvariants/Promtact/.github/workflows/release.yml@refs/tags/v0.3.6

The installer left PATH and the shell profile unchanged.

GitHub's public attestation API returned the release attestation without an
Authorization header. Its Snappy-compressed response was retained, decoded to
a Sigstore v0.3 bundle, and verified locally with the downloaded trusted root.
On this empty VM, `gh attestation download` requested an authenticated session.

## Network-isolated execution

The final offline run entered a network namespace containing only an active
loopback interface. It had no external interface or default route, and a
request to `api.github.com` failed name resolution as the negative control.

Using only material already present inside the boundary, the run:

- verified the release checksum and attestation.
- reported the exact v0.3.6 release identity.
- reproduced the leader-partition scenario and trace.
- ran the complete root Go test suite.
- tested the downstream v0.3.6 consumer.
- rebuilt and tested the Rust adapter from local dependencies.
- ran the Go-to-Rust conformance tests.
- left no Rust adapter process alive.

The scenario trace was:

    0ec85bc87c9895d2432ac5954c149f47d2a1f1fed20d44a42483f12fcac6f0a4

The fault dropped 945 messages. The final isolated run completed in 54 seconds.

The passing boundary exposed only loopback, which the local server tests
require. External interfaces, routing, and name resolution remained
unavailable throughout the run.

## Root Go and cross-language gates

The complete root suite ran with Go `1.25.13`. Root vet completed in 3 seconds,
and the complete race suite completed in 41 seconds. Every tested package
passed, and the worktree remained clean.

The checked Rust adapter activated the repository's pinned Rust `1.88.0`
toolchain with matching Cargo, rustfmt, and Clippy components. Its unit,
validation, codec, JSON protocol, and token-ring tests passed.

The Go host then launched the real Rust process and checked:

- reproducibility of the seeded campaign.
- activation of the timed network partition.
- an intentional duplicate-token negative control.
- equality with the Go reference trace.

The cross-language trace was:

    77a8aabf3cbd3ad3ee4b4543d36564c02a4888940d5f05c048db37eee56bada8

The campaign ended at step 200 with one dropped message and no pending message.
Ten repeated cross-language campaigns also passed under the Go race detector,
and no adapter process remained alive.

## Formal verification

The documented `promtact verify` command downloaded the pinned TLA+ tools,
checked their recorded digest, and ran TLC without a timeout on the two-CPU VM.

TLC completed the finite state graph with no invariant violation:

| Result | Value |
|---|---:|
| States generated | 46,667,923 |
| Distinct states | 6,121,927 |
| States left on queue | 0 |
| Reported depth with two workers | 26 |
| Duration | 2 minutes 34 seconds |
| Optimistic collision estimate | `1.3E-5` |
| Actual-fingerprint estimate | `7.1E-7` |

The reported depth is from this two-worker run. A serial qualification can
report depth 25 because parallel workers may enter the next level before the
current level closes. This is bounded model checking, not an unbounded proof.

The run completed with 1.4 GiB of memory still available and without material
swap use.

## Linux storage paths

The documented io_uring probe used 32 entries and reported
`io_uring_setup: PASS`.

The explicit Linux integration gates then passed:

    PROMTACT_URING_INTEGRATION=1 go test ./storage/uring ./storage/uringwal -count=1 -v

They covered direct-I/O alignment, durable writer integration, and WAL reopen
through io_uring. The source worktree remained clean.

## Five-node failover

Five server processes started as the unprivileged `promtact` user. A write of
key 7 with value 42 was accepted through leader 1 and read back before failure.

Leader 1 was terminated. During the election the client observed temporary
connection and non-success responses. A retry through the newly elected leader
4 returned value 42 with a successful status.

All five nodes had durable WAL files. The surviving WAL files ranged from
1,008 to 1,120 bytes, while the terminated leader had persisted 560 bytes.
The servers stayed quiet on their successful paths. Client responses and
durable WAL state supplied the evidence for election, replication, and
recovery.

All remaining processes were terminated after the test, while their state
directories were retained for restore qualification.

## Backup and restore

An offline backup was created from the real node 4 data directory. The source
WAL SHA-256 was:

    66048936cad7b1a125bf64d8d90a614065df05a07665ddc3458b453f931115de

The backup image contained `manifest.json` and `raft.wal`. Restoring into a new
empty destination reproduced the WAL byte for byte. The original source hash
remained unchanged.

A restore into a non-empty destination was rejected with exit status 1 and the
error:

    backup: restore destination is not empty

For semantic validation, copies of all five retained node directories were
started as a new five-node cluster. Restored node 4 joined the cluster, leader
2 was elected, and a client read returned the previously stored value 42.
Every original WAL retained its pre-test hash. All copied cluster processes
were then stopped.

## Live kernel fault qualification

Live traffic, rather than program attachment alone, was the acceptance
criterion for the kernel fault controls. Each run inspected the active policy,
sent traffic through the isolated veth pair, measured the result, and checked
that cleanup completed.

| Control | Configuration | Observed result |
|---|---:|---|
| Baseline | BPF policy zero, no netem fault | 3 of 3 replies |
| XDP ingress drop | 10 percent | 442 of 500 replies, 11.6 percent loss |
| XDP ingress drop | 100 percent | 0 of 3 replies |
| TC egress drop | 100 percent | 0 of 3 replies |
| TC egress corruption | 100 percent | 0 of 3 replies |
| XDP source partition | block `192.0.2.1` | 0 of 3 replies |
| XDP destination partition | block `192.0.2.2` | 0 of 3 replies |
| netem delay | 100 ms | 5 of 5 replies, 120.554 ms average RTT |
| netem loss | 100 percent | 0 of 3 replies |

The 500-packet XDP sample measured 11.6 percent loss. This falls within the
expected statistical variation around the configured 10 percent rate.

For the delay run, the minimum RTT was 100.112 ms and the average was 120.554
ms. The first request measured 201.808 ms, while the remaining requests were
close to the configured 100 ms.

The netem runs left all BPF policy bytes at zero. This confirmed that delay and
netem loss remain independent from XDP and TC policy controls.

## One policy shared by both hooks

The destination-partition run inspected the live kernel objects. The pinned
policy map had ID 12, and both the XDP and TC programs referenced that same map.

The 20-byte policy value kept these controls separate:

- XDP drop probability.
- TC drop probability.
- TC corruption probability.
- Blocked IPv4 source.
- Blocked IPv4 destination.

The controller created and updated the complete map before attaching either
program. The live packet results showed that each public control affected the
intended path.

## Ownership and cleanup

The ownership boundary was exercised against three resources created before
the controller started:

1. an existing BPF pin root.
2. an existing network namespace.
3. an existing interface using the controller's host-veth name.

Each collision produced a clear refusal stating that Promtact could not claim
the resource and would not use or remove it without ownership. The existing
resource remained unchanged until the controlled teardown of the test fixture.

For the interface collision, normalized state before and after the controller
attempt had the same SHA-256:

    48b2fbd43732a2443093c8b478b9d92ff6b21cf1b6c5270504cba3f431aaea3b

A normal controller lifecycle then returned 3 of 3 baseline replies, showing
that the clearer refusal path did not change successful operation.

Every completed run left the namespace, pin root, host veth, and controller
process absent. The management interface `ens33` remained available throughout
the matrix.

## Installation corrections confirmed

The qualification produced two documentation corrections and then verified
both from the published commit.

First, the downstream module now pins `go1.25.13`. A fresh home with empty
module and build caches selected that version on the first Go command, verified
the public v0.3.6 module, and passed tests, vet, and twenty repeats.

Second, the Ubuntu BPF prerequisites now name `linux-tools-common`, the package
that provides `/usr/sbin/bpftool` on Ubuntu 24.04. The documented package
command completed unchanged, and the documented Clang command produced a valid
eBPF object.

## Qualification result

The VM reproduced the public installation paths, release identity, deterministic
traces, cross-language behavior, formal state-space result, Linux storage
paths, five-node failover, semantic restore, and live kernel fault effects.

Taken together, these gates covered deterministic simulation, a real Rust
subprocess, durable node state, multi-process failover, formal safety bounds,
release provenance, offline operation, and measured kernel packet effects on
one fresh machine.

## Claim boundaries

These results apply to the named releases, commits, VM, kernel, toolchains, and
finite workloads recorded here. The XDP percentage is a bounded sample, storage
latency is not measured by this VM report, and the TLA+ result covers its
configured finite state space.

Backup and restore were exercised offline. The five-node recovery check used
copies of retained WAL state. No claim is made here about online backup.

Promtact remains self-hosted software rather than a managed service. This
qualification is engineering evidence, not a third-party penetration test,
SOC 2 audit, or ISO 27001 certification.
