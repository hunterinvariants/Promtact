# Kernel chaos layer

Promtact provides verifier-checked Linux eBPF programs for controlled packet
faults inside an isolated network namespace:

- XDP ingress drop with an independently configured probability.
- XDP ingress partitions by exact IPv4 source or destination.
- TC egress drop with its own probability.
- TC egress corruption with its own probability.
- `netem` delay and loss, configured independently from the BPF policy.

The XDP and TC programs share one pinned array map. Promtact creates the map,
writes the complete policy value in one update, and then loads and attaches both
programs against that map. Both hooks use the same policy for the lifetime
of a run.

Probabilities are accepted as percentages from 0 through 100 and encoded at
1/10,000 resolution. XDP drop, TC drop, TC corruption, and `netem` loss remain
separate controls.

## Build

On Ubuntu, install the Linux BPF build and control tools:

    sudo apt-get update
    sudo apt-get install -y \
      clang llvm libbpf-dev linux-libc-dev gcc bpftool iproute2 util-linux

Compile the checked-in program:

    clang \
      -O2 \
      -g \
      -target bpf \
      -D__TARGET_ARCH_x86 \
      -I/usr/include/$(gcc -dumpmachine) \
      -c bpf/promtact_chaos.bpf.c \
      -o /tmp/promtact_chaos.bpf.o

The Linux kernel workflow runs the same compilation command.

## Run

The controller requires root because it creates a network namespace, a veth
pair, BPF pins, XDP and TC attachments, and optional `netem` qdiscs.

For example:

    sudo go run ./cmd/promtact chaos \
      -bpf-object /tmp/promtact_chaos.bpf.o \
      -xdp-drop 10 \
      -tc-drop 2.5 \
      -tc-corrupt 0.5 \
      -delay 25ms \
      -loss 1 \
      -yes-really

IPv4 partitions can be added independently:

    sudo go run ./cmd/promtact chaos \
      -bpf-object /tmp/promtact_chaos.bpf.o \
      -block-source 192.0.2.1 \
      -block-destination 192.0.2.2 \
      -yes-really

The command remains in the foreground while the fault environment is active.
An interrupt or termination signal detaches the programs and removes the
resources created by that controller.

## Safety and ownership

The controller enforces a dedicated `promtact-chaos` namespace and
`promtact-*` veth names. It rejects unsafe names, invalid addresses,
non-finite values, and percentages outside the supported range.

The pin root acts as an ownership boundary. Existing pins, namespaces, or
interfaces are never adopted or deleted. Cleanup removes only resources created
by the active controller, is safe to repeat, and reports unexpected pin
contents instead of hiding them.

When attaching the programs, Promtact enters only the target network namespace.
The host mount namespace remains available for resolving the pinned programs,
and the management interface is never part of the plan.

Never attach a development fault policy directly to a host management
interface.

## Verification

The checked tests cover policy encoding, input bounds, command ordering,
shared-map reuse, resource collisions, rollback, and idempotent cleanup:

    go test ./chaos ./internal/cli

The Linux kernel workflow also compiles the eBPF object and tests the Go
controller and kernel-facing packages.
