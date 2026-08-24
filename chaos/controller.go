// Package chaos controls kernel fault injection inside an isolated netns.
package chaos

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"time"
)

const bpfPinBase = "/sys/fs/bpf"

var safeName = regexp.MustCompile(`^promtact-[a-zA-Z0-9_-]{1,32}$`)

type Plan struct {
	Namespace string
	HostVeth  string
	PeerVeth  string
	HostCIDR  string
	PeerCIDR  string
	BPFObject string
	Policy    Policy
	Delay     time.Duration
	LossPct   float64
}

func (p Plan) Validate() error {
	if !safeName.MatchString(p.Namespace) ||
		!safeName.MatchString(p.HostVeth) ||
		!safeName.MatchString(p.PeerVeth) {
		return errors.New("chaos: namespace and interfaces must start with promtact-")
	}
	if p.HostVeth == p.PeerVeth {
		return errors.New("chaos: host and peer interfaces must be distinct")
	}
	if p.BPFObject == "" {
		return errors.New("chaos: BPF object is required")
	}
	hostIP, hostNet, err := net.ParseCIDR(p.HostCIDR)
	if err != nil {
		return errors.New("chaos: invalid host CIDR")
	}
	peerIP, peerNet, err := net.ParseCIDR(p.PeerCIDR)
	if err != nil || !hostNet.Contains(peerIP) || !peerNet.Contains(hostIP) || hostIP.Equal(peerIP) {
		return errors.New("chaos: peer CIDR must be distinct and in the same subnet")
	}
	if p.Delay < 0 || p.Delay > time.Minute ||
		math.IsNaN(p.LossPct) || math.IsInf(p.LossPct, 0) ||
		p.LossPct < 0 || p.LossPct > 100 {
		return errors.New("chaos: delay/loss outside safety bounds")
	}
	return p.Policy.Validate()
}

type Runner interface {
	Run(context.Context, string, ...string) error
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %v: %w: %s", name, args, err, output)
	}
	return nil
}

type pinPaths struct {
	root     string
	programs string
	maps     string
	xdp      string
	tc       string
	policy   string
}

func pinsFor(namespace string) pinPaths {
	root := path.Join(bpfPinBase, namespace)
	programs := path.Join(root, "programs")
	maps := path.Join(root, "maps")
	return pinPaths{
		root:     root,
		programs: programs,
		maps:     maps,
		xdp:      path.Join(programs, "promtact_xdp"),
		tc:       path.Join(programs, "promtact_tc"),
		policy:   path.Join(maps, "policy"),
	}
}

type Controller struct {
	plan          Plan
	runner        Runner
	active        bool
	ownsPinRoot   bool
	ownsNamespace bool
	ownsHostVeth  bool
}

func New(plan Plan, runner Runner) (*Controller, error) {
	if plan.HostCIDR == "" {
		plan.HostCIDR = "192.0.2.1/30"
	}
	if plan.PeerCIDR == "" {
		plan.PeerCIDR = "192.0.2.2/30"
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if runner == nil {
		return nil, errors.New("chaos: runner is required")
	}
	return &Controller{plan: plan, runner: runner}, nil
}

type commandStep struct {
	arguments []string
	completed func()
	claim     string
}

func (c *Controller) Apply(ctx context.Context) error {
	if c.active {
		return errors.New("chaos: plan already active")
	}

	p := c.plan
	policyValue, err := p.Policy.mapValue()
	if err != nil {
		return err
	}
	pins := pinsFor(p.Namespace)
	netnsPath := path.Join("/var/run/netns", p.Namespace)
	netnsArg := "--net=" + netnsPath

	steps := []commandStep{
		{
			arguments: []string{"mkdir", "--", pins.root},
			claim:     fmt.Sprintf("pin root %q", pins.root),
			completed: func() {
				c.ownsPinRoot = true
			},
		},
		{
			arguments: []string{"mkdir", "--", pins.programs, pins.maps},
		},
		{
			arguments: []string{"bpftool", "map", "create", pins.policy,
				"type", "array", "key", "4", "value", strconv.Itoa(policyMapValueSize),
				"entries", "1", "name", "policy"},
		},
		{
			arguments: policyUpdateStep(pins.policy, policyValue),
		},
		{
			arguments: []string{"bpftool", "prog", "loadall", p.BPFObject, pins.programs,
				"map", "name", "policy", "pinned", pins.policy},
		},
		{
			arguments: []string{"ip", "netns", "add", p.Namespace},
			claim:     fmt.Sprintf("namespace %q", p.Namespace),
			completed: func() {
				c.ownsNamespace = true
			},
		},
		{
			arguments: []string{"ip", "link", "add", p.HostVeth,
				"type", "veth", "peer", "name", p.PeerVeth},
			claim: fmt.Sprintf("veth pair %q and %q", p.HostVeth, p.PeerVeth),
			completed: func() {
				c.ownsHostVeth = true
			},
		},
		{
			arguments: []string{"ip", "link", "set", p.PeerVeth, "netns", p.Namespace},
		},
		{
			arguments: []string{"ip", "addr", "add", p.HostCIDR, "dev", p.HostVeth},
		},
		{
			arguments: []string{"ip", "link", "set", p.HostVeth, "up"},
		},
		{
			arguments: []string{"ip", "netns", "exec", p.Namespace,
				"ip", "link", "set", "lo", "up"},
		},
		{
			arguments: []string{"ip", "netns", "exec", p.Namespace,
				"ip", "addr", "add", p.PeerCIDR, "dev", p.PeerVeth},
		},
		{
			arguments: []string{"ip", "netns", "exec", p.Namespace,
				"ip", "link", "set", p.PeerVeth, "up"},
		},
		{
			arguments: []string{"nsenter", netnsArg, "--", "ip", "link", "set", p.PeerVeth,
				"xdp", "pinned", pins.xdp},
		},
		{
			arguments: []string{"nsenter", netnsArg, "--", "tc",
				"qdisc", "add", "dev", p.PeerVeth, "clsact"},
		},
		{
			arguments: []string{"nsenter", netnsArg, "--", "tc",
				"filter", "add", "dev", p.PeerVeth,
				"egress", "bpf", "object-pinned", pins.tc, "direct-action"},
		},
	}
	if p.Delay > 0 || p.LossPct > 0 {
		steps = append(steps, commandStep{
			arguments: []string{"nsenter", netnsArg, "--", "tc",
				"qdisc", "add", "dev", p.PeerVeth, "root", "netem",
				"delay", p.Delay.String(),
				"loss", strconv.FormatFloat(p.LossPct, 'f', 3, 64) + "%"},
		})
	}

	for _, step := range steps {
		if err := c.runner.Run(ctx, step.arguments[0], step.arguments[1:]...); err != nil {
			failure := err
			if step.claim != "" {
				failure = fmt.Errorf("chaos: cannot claim %s; refusing to use or remove it without ownership: %w", step.claim, err)
			}
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return errors.Join(failure, c.cleanup(cleanupCtx))
		}
		if step.completed != nil {
			step.completed()
		}
	}

	c.active = true
	return nil
}

func policyUpdateStep(policyPin string, value []byte) []string {
	step := []string{
		"bpftool", "map", "update", "pinned", policyPin,
		"key", "hex", "00", "00", "00", "00",
		"value", "hex",
	}
	for _, octet := range value {
		step = append(step, fmt.Sprintf("%02x", octet))
	}
	return append(step, "any")
}

func (c *Controller) Close(ctx context.Context) error {
	err := c.cleanup(ctx)
	if err == nil {
		c.active = false
	}
	return err
}

func (c *Controller) cleanup(ctx context.Context) error {
	pins := pinsFor(c.plan.Namespace)
	var cleanupErrors []error

	hadNamespace := c.ownsNamespace
	if hadNamespace {
		if err := c.runner.Run(ctx, "ip", "netns", "del", c.plan.Namespace); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		} else {
			c.ownsNamespace = false
		}
	}

	if c.ownsHostVeth {
		err := c.runner.Run(ctx, "ip", "link", "del", c.plan.HostVeth)
		if err == nil || hadNamespace && !c.ownsNamespace {
			c.ownsHostVeth = false
		} else {
			cleanupErrors = append(cleanupErrors, err)
		}
	}

	if c.ownsPinRoot {
		if err := c.runner.Run(ctx, "rm", "-f", "--",
			pins.xdp, pins.tc, pins.policy); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
		if err := c.runner.Run(ctx, "rmdir", "--",
			pins.programs, pins.maps, pins.root); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		} else {
			c.ownsPinRoot = false
		}
	}

	return errors.Join(cleanupErrors...)
}
