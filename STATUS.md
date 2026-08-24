# Project status

Updated: 2026-08-17

## Completed and verified

| Area | Capability | Evidence |
|---|---|---|
| DST | deterministic clock, network, seeds, crash/restart | reproducibility and committed-prefix tests |
| Raft | pre-vote, duplicate-safe elections, replication, commit | randomized safety tests |
| Raft | compacted absolute indexes and durable snapshot catch-up | unit, crash/restart, DST, Sentinel io_uring gates |
| Raft | replicated joint/final configuration transitions | dual-majority, restart, removal, and five-node DST tests |
| Raft | quorum-confirmed ReadIndex and leadership transfer | protocol and race tests |
| Persistence | snapshot-before-WAL-fence compaction ordering | interrupted-install recovery and replay tests |
| DST storage | bit-rot, misdirected writes, phantom prefixes | seeded fault/recovery tests |
| Persistence | term/vote before vote response | fail-stop ordering tests |
| Persistence | entry durable before AppendEntries ACK | fail-stop ordering tests |
| WAL | CRC32C, monotonic sequence, torn-tail recovery | exhaustive byte-cut tests |
| Recovery | durable WAL reconstructs node state | restart matrix and replay tests |
| io_uring | setup and ring mapping | Sentinel integration pass |
| io_uring | registered file and buffer, `WRITE_FIXED`, CQE, `FSYNC` | Sentinel integration pass |
| Direct I/O | aligned 4096-byte `O_DIRECT` blocks | Sentinel integration pass |
| WAL + io_uring | write, close, reopen, validate, replay | `storage/uringwal` integration pass |
| Kernel chaos policy | shared XDP/TC map, input bounds, ownership, and cleanup | unit and real-kernel pass |
| Kernel chaos effects | XDP drop/partition, TC drop/corruption, and `netem` delay/loss | Sentinel live qualification |
| Phase 3 | Linux io_uring, direct NVMe I/O, XDP/TC chaos, safe cleanup | complete; Sentinel evidence |
| Snapshot format | checksummed encode/decode and torn-image rejection | unit tests |
| Membership | old/new joint-majority calculation | unit tests |
| Tooling | seed sweeper, race suite, fuzz target, CI | executable commands/workflows |
| Formal | bounded election, append, commit, snapshot, membership, and crash TLA+ model | 6,121,927 distinct states; all invariants pass |
| Service | versioned peer/client protocol and multi-process TCP cluster | protocol and three-process restart tests |
| Client safety | replicated request IDs, deterministic deduplication, ReadIndex reads | restart and failover tests |
| Operations | bounded queues, health, metrics, shutdown, backup/restore | unit and integration tests |
| Jepsen | live register workload and Knossos checker | Sentinel `valid? true` under process and TC network faults |
| NVMe | raw `O_DIRECT` + registered io_uring durability on `/dev/nvme0n1` | 10,000 writes, 7,971 ops/s, p99 196.343 us, all gates pass |

Additional measured evidence:

- 1,000 seeds x 1,000 virtual ticks with periodic restart: pass;
- WAL encode benchmark: 0 B/op, 0 allocs/op;
- Raft heartbeat Step: 0 allocations across 10,000 measured runs;
- 1,000 durable `WRITE_FIXED + FSYNC` operations to a file: 2,189 ops/s,
  p50 449.616 us, p99 543.602 us, max 1.517617 ms;
- Phase 4 Sentinel race gate: pass;
- 100x deterministic five-node Raft/DST gate: pass in 481.973 seconds;
- 100x io_uring snapshot/compaction/recovery gate: pass;
- Phase 5 five-process Sentinel gate: pass;
- Jepsen/Knossos register history under process and TC faults: `valid? true`.
- raw NVMe gate: 10,000 durable operations, p50 117.207 us, p99 196.343 us,
  max 13.668893 ms; race, TLC, and 100x integration gates pass.
- Phase 6 Sentinel gate: ENOSPC/EIO/corruption/restart/SIGKILL pass;
  Jepsen/Knossos `valid? true`; bounded TLC 46,667,923 generated and
  6,121,927 distinct states with no invariant violation.

## Post-release engineering backlog

These are optimizations and broader deployment work, not incomplete roadmap
acceptance gates:

- batched SQE submission, group commit, and queue-depth tuning;
- format migration tooling and broader operational automation;
- qualification of additional kernels, controllers, and machine types.

## Claim policy

Promtact has completed all six scoped roadmap phases and their recorded
acceptance gates. It is a verification-focused reference system, not a turnkey
managed database service. Claims remain bounded by the checked-in evidence:
zero-allocation is established only for the measured hot paths, latency only
for the named devices, Jepsen linearizability only for the
recorded workload, and TLA+ safety only for the documented finite bounds.