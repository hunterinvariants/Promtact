# Operating envelope

Promtact is qualified only within the following recorded envelope.

- Linux amd64 with kernel 6.8 or newer and available `io_uring`.
- Files or dedicated block devices must support aligned 4096-byte `O_DIRECT`.
- A successful WAL append means both the `WRITE_FIXED` CQE and subsequent
  `FSYNC` CQE completed successfully.
- Storage corruption, sequence mismatch, failed append, failed sync, malformed
  configuration, and failed snapshot persistence cause fail-stop behavior.
- Deployments require an odd voting set of at least three independent nodes.
- Client request IDs must be stable across retries; server queues are bounded.
- XDP/TC chaos tooling may only use dedicated `promtact-*` namespaces and
  interfaces with one shared policy map. Explicit ownership protects existing
  resources, and management interfaces remain outside the supported envelope.
- Backup is offline and checksummed. Restore must target an empty data
  directory and be validated before the node rejoins a cluster.

Measured configurations and their evidence are under `benchmarks/`. Results
must not be generalized to different kernels, storage controller, cloud machine
types, durability policies, or queue depths.

Not qualified: Byzantine peers, multi-datacenter timing assumptions,
rolling format migration or
sub-microsecond durable latency.
