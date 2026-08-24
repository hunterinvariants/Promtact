//go:build linux

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hunterinvariants/promtact/chaos"
)

type chaosOptions struct {
	object             string
	delay              time.Duration
	netemLoss          float64
	xdpDrop            float64
	tcDrop             float64
	tcCorrupt          float64
	blockedSource      string
	blockedDestination string
	confirm            bool
}

func chaosCommand() Command {
	return Command{
		Name:    "chaos",
		Summary: "apply kernel fault injection inside a dedicated namespace",
		Run:     runChaos,
	}
}

func newChaosFlagSet(output io.Writer) (*flag.FlagSet, *chaosOptions) {
	options := &chaosOptions{}
	flags := flag.NewFlagSet("promtact-chaos", flag.ContinueOnError)
	flags.SetOutput(output)

	flags.StringVar(&options.object, "bpf-object", "", "compiled promtact_chaos.bpf.o")
	flags.DurationVar(&options.delay, "delay", 0, "netem delay")
	flags.Float64Var(&options.netemLoss, "loss", 0, "netem loss percentage")
	flags.Float64Var(&options.xdpDrop, "xdp-drop", 0, "XDP ingress drop percentage")
	flags.Float64Var(&options.tcDrop, "tc-drop", 0, "TC egress drop percentage")
	flags.Float64Var(&options.tcCorrupt, "tc-corrupt", 0, "TC egress corruption percentage")
	flags.StringVar(&options.blockedSource, "block-source", "", "XDP blocked IPv4 source")
	flags.StringVar(&options.blockedDestination, "block-destination", "", "XDP blocked IPv4 destination")
	flags.BoolVar(&options.confirm, "yes-really", false, "required safety acknowledgement")

	return flags, options
}

func (o chaosOptions) plan() (chaos.Plan, error) {
	blockedSource, err := optionalChaosIPv4("blocked source", o.blockedSource)
	if err != nil {
		return chaos.Plan{}, err
	}
	blockedDestination, err := optionalChaosIPv4("blocked destination", o.blockedDestination)
	if err != nil {
		return chaos.Plan{}, err
	}

	plan := chaos.Plan{
		Namespace: "promtact-chaos",
		HostVeth:  "promtact-host",
		PeerVeth:  "promtact-peer",
		HostCIDR:  "192.0.2.1/30",
		PeerCIDR:  "192.0.2.2/30",
		BPFObject: o.object,
		Policy: chaos.Policy{
			XDPDropPct:   o.xdpDrop,
			TCDropPct:    o.tcDrop,
			TCCorruptPct: o.tcCorrupt,
			BlockedSrc:   blockedSource,
			BlockedDst:   blockedDestination,
		},
		Delay:   o.delay,
		LossPct: o.netemLoss,
	}
	if err := plan.Validate(); err != nil {
		return chaos.Plan{}, err
	}
	return plan, nil
}

func optionalChaosIPv4(name, value string) (netip.Addr, error) {
	if value == "" {
		return netip.Addr{}, nil
	}
	address, err := netip.ParseAddr(value)
	if err != nil || !address.Is4() {
		return netip.Addr{}, fmt.Errorf("chaos: %s must be an IPv4 address", name)
	}
	return address, nil
}

func runChaos(args []string) int {
	flags, options := newChaosFlagSet(os.Stderr)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if !options.confirm {
		fmt.Fprintln(os.Stderr, "refusing privileged network changes without -yes-really")
		return 2
	}

	plan, err := options.plan()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	controller, err := chaos.New(plan, chaos.ExecRunner{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := controller.Apply(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	fmt.Println("chaos namespace active; test with: ping 192.0.2.2")
	fmt.Println("interrupt to detach programs and clean up")
	<-ctx.Done()

	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := controller.Close(cleanupCtx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
