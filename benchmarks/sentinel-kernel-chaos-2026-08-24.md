# Sentinel kernel chaos qualification, 2026-08-24

This run exercised Promtact's XDP, TC, IPv4 partition, `netem`, policy-map,
ownership, and cleanup paths against the Linux kernel.

All packet tests used the dedicated `promtact-chaos` namespace and the
reserved `192.0.2.0/30` test network. The management interface `ens33` and its
`192.168.183.0/24` network were outside the fault plan.

## Environment

| Item | Value |
|---|---|
| Host | Sentinel |
| Operating system | Ubuntu 24.04.4 LTS |
| Kernel | 6.8.0-138-generic |
| Architecture | x86_64 |
| Logical CPUs | 4 |
| Go | 1.25.13 |
| Clang | Ubuntu 18.1.3 |
| bpftool | 7.4.0 with libbpf 1.4 |
| ip and tc | iproute2 6.1.0 with libbpf 1.3.0 |
| nsenter | util-linux 2.39.3 |
| Management interface | ens33, 192.168.183.132/24 |
| Test network | 192.0.2.1/30 and 192.0.2.2/30 |

## Tested source identity

The following hashes identify the working-tree sources used for the kernel
runs:

| File | SHA-256 |
|---|---|
| `bpf/promtact_chaos.bpf.c` | `647c811f65139bb2ef74becf22b7a2262d6de42fa395c2d5ad5860d87adfb3f1` |
| `chaos/controller.go` | `818295fe724a23b09c572af3121062e187d8092147a4dbb20236d886525bbea4` |
| `chaos/policy.go` | `65abfdd66533681b1950768d4bc5e0104282c9ab7f905d61382e1e28eb1b5ba4` |
| `internal/cli/chaos_linux.go` | `d503f04cdd5665227a43162c7c89a584d3bca6c539440cbafd2721492f15f4f4` |

## Packet effects

The following observations came from traffic between `192.0.2.1` and
`192.0.2.2` through the isolated veth pair:

| Control | Configuration | Observation |
|---|---:|---|
| Zero policy | All BPF rates and addresses zero | 3 of 3 ICMP requests returned |
| XDP ingress drop | 100% | 0 of 3 ICMP requests returned |
| XDP ingress drop | 10% | 454 of 500 returned, for 9.2% observed loss |
| TC egress drop | 100% | 0 of 3 ICMP requests returned |
| TC egress corruption | 100% | 0 of 3 ICMP requests returned |
| XDP source partition | Block 192.0.2.1 | 0 of 3 ICMP requests returned |
| netem delay | 100 ms | 5 of 5 returned, with 120.434 ms average RTT |
| netem loss | 100% | 0 of 3 ICMP requests returned |

The 10% XDP run is a bounded statistical observation rather than a claim about
every future sample. Its measured 9.2% loss was consistent with the configured
rate.

The 100 ms `netem` run reported a minimum RTT of 100.275 ms, an average of
120.434 ms, and a maximum of 200.593 ms. The first request included queue
startup effects; the remaining requests were close to 100 ms.

## Shared policy map

The controller created one 20-byte array-map value before loading either
program. Kernel inspection showed that the pinned XDP and TC programs referenced
the same map ID.

The live map values also matched each requested control:

- 100% XDP drop encoded 10,000 at byte offset 0.
- 100% TC drop encoded 10,000 at byte offset 4.
- 100% TC corruption encoded 10,000 at byte offset 8.
- blocked source `192.0.2.1` occupied bytes 12 through 15.
- the `netem`-only run left all 20 BPF policy bytes at zero.

No fault was configured on `ens33`.

## Ownership and cleanup

Three real controller lifecycles exercised the ownership boundary:

1. An existing pin root caused the controller to stop without changing or
   removing that directory.
2. An existing network namespace caused the controller to stop without deleting
   the namespace. Pins created before the collision were removed.
3. A normal run created the namespace, veth pair, map, and both programs. After
   `SIGTERM`, the process exited successfully and removed every owned resource.

The final checks found no `promtact-chaos` namespace, `promtact-host` veth,
`/sys/fs/bpf/promtact-chaos` pin root, or controller process.

The normalized management-network snapshot had the same SHA-256 before and
after the completed `netem` run:

    c2093c5fa04a39da8e2ce3b55b0f5f550e5caeb9a6bde9765d65abeb5a30234f

## Reproduction gates

The source-level gates were:

    go test ./chaos ./internal/cli ./storage/uring
    go build ./cmd/promtact-chaos ./cmd/promtact
    clang -O2 -g -target bpf -D__TARGET_ARCH_x86 \
      -I/usr/include/$(gcc -dumpmachine) \
      -c bpf/promtact_chaos.bpf.c \
      -o /tmp/promtact_chaos.bpf.o

The real-kernel checks inspected the pinned map and program IDs with `bpftool`,
the XDP attachment with `ip`, the TC filter and qdisc with `tc`, and packet
effects with `ping`.

## Result

The shared policy map controlled both BPF hooks, every configured fault produced
the expected packet effect, collisions preserved resources owned by another
run, and normal termination left no kernel-chaos resources behind. The
management interface remained outside every fault plan.
