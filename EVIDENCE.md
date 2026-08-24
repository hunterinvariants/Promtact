# Promtact evidence

Status: **Specification & Qualification Complete**

This file is the evidence index. Every result is tied to a named configuration
or a finite verification bound.

## Qualification summary

| Area | Result | Evidence |
|---|---|---|
| Raft and DST | crash, restart, snapshot, compaction, membership, ReadIndex, and leadership-transfer gates pass | [Sentinel qualification run](benchmarks/sentinel-2026-08-17.md) |
| Distributed service | five-process failover, backup/restore, bounded backpressure, metrics, and shutdown gates pass | [Sentinel qualification run](benchmarks/sentinel-2026-08-17.md) |
| Linearizability | Jepsen/Knossos register history reports `valid? true` during process and network faults, on the qualification host and in CI | [Sentinel qualification run](benchmarks/sentinel-2026-08-17.md), [jepsen workflow](.github/workflows/jepsen.yml) |
| Storage faults | ENOSPC, append EIO, sync EIO, torn writes, bit rot, misdirected writes, phantom reads, and fail-stop ACK rules pass | [Sentinel qualification run](benchmarks/sentinel-2026-08-17.md) |
| Kernel paths | registered file/buffer, `O_DIRECT`, `WRITE_FIXED`, CQE validation, `FSYNC`, shared XDP/TC policy, live packet effects, and collision-safe cleanup pass | [Sentinel qualification run](benchmarks/sentinel-2026-08-17.md), [kernel chaos qualification](benchmarks/sentinel-kernel-chaos-2026-08-24.md) |
| Fresh Ubuntu VM | public installation, release provenance, offline execution, downstream adoption, cross-language behavior, failover, restore, and kernel fault controls pass together | [Ubuntu VM qualification](benchmarks/ubuntu-vm-qualification-2026-08-24.md) |
| NVMe | 10,000 raw durable operations pass on the named NVMe device | [Sentinel qualification run](benchmarks/sentinel-2026-08-17.md) |
| Formal model | bounded TLC exploration completes with no invariant violation | [Sentinel qualification run](benchmarks/sentinel-2026-08-17.md) |

## Framework evidence

The engine, its invariants, its fault injection, the scenario format, and the
storage conformance suite sit on top of the consensus core and are not part of
any roadmap phase. They claim no phase acceptance and carry their own gate
evidence:

| Area | Result | Evidence |
|---|---|---|
| Deterministic engine | 7,000 paired runs compare `dst.Engine` against `sim.Simulator` at every tick and require a bit-identical execution | [Engine qualification](benchmarks/sentinel-dst-engine-2026-08-17.md) |
| Invariants and faults | packaged Raft properties, leader partition, and one-way link failure hold across 1,000 seeds each | [Engine qualification](benchmarks/sentinel-dst-engine-2026-08-17.md) |
| Storage conformance | `MemoryDevice` and `FileDevice` pass the ten `wal.Device` properties | [Engine qualification](benchmarks/sentinel-dst-engine-2026-08-17.md) |

The engine equivalence is a comparison between two implementations on one host;
it establishes nothing about cross-platform trace stability. The invariants are
safety properties, with no liveness property checked.

## NVMe result

Configuration: Ubuntu 24.04.4 LTS, kernel `6.8.0-137-generic`, `/dev/nvme0n1`
at 20 GiB, 4 KiB blocks, registered io_uring file and buffer, `O_DIRECT`,
`WRITE_FIXED`, CQE validation, and one completed `FSYNC` per operation.

| Operations | Throughput | p50 | p99 | Maximum |
|---:|---:|---:|---:|---:|
| 10,000 | 7,971 ops/s | 117.207 us | 196.343 us | 13.668893 ms |

These numbers describe this device in this machine; they are not generalized
hardware claims.

## Linearizability result

The live five-node workload used Jepsen with the Knossos register checker.
During the measured history, the harness killed the leader process and applied
complete network loss to an isolated node. The final checker result was:

```clojure
{:linearizable {:valid? true}
 :timeline {:valid? true}
 :valid? true}
```

Recorded result:
`jepsen/store/promtact-live-linearizability/20260817T172938.145+0200/results.edn`.

Knossos is the checker this repository uses; no Porcupine result is claimed.

The same property is now checked continuously rather than only on the
qualification host. `.github/workflows/jepsen.yml` builds a five-node cluster in
network namespaces, kills the leader, applies total loss to a second node, and
requires Knossos to report a valid history. It runs weekly, on any change to the
server, the client or the workload, and on demand. Two guards keep a pass from
being empty: the verdict is read out of the checker's output rather than taken
from the exit code, and a history with no operations fails.

That run is a shorter workload on a shared runner. It does not replace the
recorded result above, which remains the qualification evidence; it establishes
that the property still holds after a change, which one recorded run cannot.

## TLA+ state-space result

TLC exhaustively explored the configured finite model:

| Bound or result | Value |
|---|---:|
| Nodes | 3 |
| Maximum term | 2 |
| Maximum log length | 2 |
| States generated | 46,667,923 |
| Distinct states | 6,121,927 |
| State-graph depth | 25 |
| States remaining | 0 |
| Invariant violations | 0 |

Modeled transitions include durable election, AppendEntries replication,
current-term commit, compaction, InstallSnapshot, joint and final membership,
and crash recovery. Checked invariants cover type correctness, election safety,
committed-prefix safety, snapshot safety, and durable-vote safety.

The depth is the value from a serial run, which is the reliable one: under
`-workers auto` TLC can report one level more, because a worker may reach the
next level before the current one closes.

Fingerprint-collision estimates were `1.3E-5` optimistic and `8.3E-7` from the
actual fingerprints. The second figure is a property of the run, since TLC draws
a fresh fingerprint seed each time. This is bounded model checking, not an
unbounded proof.

## Claim boundary

The repository is a specification-and-qualification-complete reference
implementation. Zero-allocation applies to measured hot paths; latency applies
to the named configuration; linearizability applies to the recorded workload;
and formal safety applies to the documented finite bounds.

See [STATUS.md](STATUS.md), [ROADMAP.md](ROADMAP.md),
[docs/OPERATING-ENVELOPE.md](docs/OPERATING-ENVELOPE.md), and
[docs/RELEASE-CHECKLIST.md](docs/RELEASE-CHECKLIST.md).
