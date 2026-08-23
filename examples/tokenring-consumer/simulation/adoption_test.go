package simulation

import (
	"reflect"
	"testing"

	"github.com/hunterinvariants/promtact/dst"
	"github.com/hunterinvariants/promtact/examples/tokenring-consumer/tokenring"
)

type runResult struct {
	trace      string
	passes     uint64
	pending    int
	holders    []uint32
	faultName  string
	faultDrops map[string]int
}

func runTokenRing(seed int64, withPartition bool) (runResult, error) {
	cluster, err := newCluster(3)
	if err != nil {
		return runResult{}, err
	}

	engine := dst.New(
		dst.Config{
			Seed:     seed,
			MaxDelay: 3,
		},
		cluster,
		wire{},
	)

	engine.Watch(atMostOneToken(cluster.ring))

	faultName := ""
	if withPartition {
		partition := dst.During(
			20,
			60,
			dst.Split(
				[]uint32{1},
				[]uint32{2, 3},
			),
		)

		faultName = partition.Name()
		engine.Inject(partition)
	}

	runErr := engine.RunChecked(200)

	return runResult{
		trace:      engine.TraceHash(),
		passes:     cluster.ring.Passes(),
		pending:    engine.Pending(),
		holders:    cluster.ring.HolderIDs(),
		faultName:  faultName,
		faultDrops: engine.InjectedDrops(),
	}, runErr
}

func TestSeededRunIsReproducible(t *testing.T) {
	const seed int64 = 0x4a2c

	first, err := runTokenRing(seed, false)
	if err != nil {
		t.Fatalf("first run failed: %v", err)
	}

	second, err := runTokenRing(seed, false)
	if err != nil {
		t.Fatalf("second run failed: %v", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatalf(
			"identical seeds diverged:\nfirst:  %#v\nsecond: %#v",
			first,
			second,
		)
	}

	if first.passes == 0 {
		t.Fatal("run completed without passing the token")
	}

	if first.trace == "" {
		t.Fatal("run returned an empty trace hash")
	}

	t.Logf(
		"seed=%d passes=%d pending=%d holders=%v trace=%s",
		seed,
		first.passes,
		first.pending,
		first.holders,
		first.trace,
	)
}

func TestTimedPartitionIsDeterministicAndNonVacuous(t *testing.T) {
	const seed int64 = 0x71e5

	first, err := runTokenRing(seed, true)
	if err != nil {
		t.Fatalf("first partitioned run failed: %v", err)
	}

	second, err := runTokenRing(seed, true)
	if err != nil {
		t.Fatalf("second partitioned run failed: %v", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatalf(
			"partitioned runs diverged:\nfirst:  %#v\nsecond: %#v",
			first,
			second,
		)
	}

	if first.faultName == "" {
		t.Fatal("partition run did not record a fault name")
	}

	if first.faultDrops[first.faultName] == 0 {
		t.Fatalf(
			"fault %q never dropped a message; result would be vacuous",
			first.faultName,
		)
	}

	if len(first.holders) > 1 {
		t.Fatalf("multiple token holders after partition: %v", first.holders)
	}

	t.Logf(
		"seed=%d fault=%q drops=%d passes=%d pending=%d holders=%v trace=%s",
		seed,
		first.faultName,
		first.faultDrops[first.faultName],
		first.passes,
		first.pending,
		first.holders,
		first.trace,
	)
}

var _ dst.Cluster[tokenring.Message] = (*cluster)(nil)
