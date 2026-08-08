package store

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/hunterinvariants/promtact/internal/witness"
)

// Storage for witness receipts.
//
// The receipt is the only artefact here that is worth more the older it gets. A
// chain head proves nothing on its own; a third party's signature over that
// head, kept locally, lets someone check a year later that this history is the
// one that was witnessed - without the witness being reachable, and without
// trusting whoever hands over the database.
//
// Receipts are therefore never pruned by retention. Everything else in this
// system ages out; deleting the proof that a range was witnessed would recreate
// exactly the hole the receipt exists to fill, and would do it on a schedule.

// SaveWitnessReceipt records a receipt. Receipts are immutable: an index that
// has already been witnessed keeps its first receipt, because a second one for
// the same index either says the same thing or is the disagreement this system
// exists to surface - and in neither case should it quietly overwrite.
func (s *Store) SaveWitnessReceipt(receipt witness.Receipt) error {
	if s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.receipts == nil {
			s.receipts = make(map[int]witness.Receipt)
		}
		if _, exists := s.receipts[receipt.ChainIndex]; !exists {
			s.receipts[receipt.ChainIndex] = receipt
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.db.ExecContext(ctx, `
INSERT INTO promtact_witness_receipts (chain_index, head, witnessed_at, signature, key_id)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (chain_index) DO NOTHING`,
		receipt.ChainIndex, strings.ToLower(strings.TrimSpace(receipt.Head)),
		strings.TrimSpace(receipt.WitnessedAt), receipt.Signature, receipt.KeyID)
	return err
}

// WitnessReceipts returns every stored receipt, oldest first.
func (s *Store) WitnessReceipts() ([]witness.Receipt, error) {
	if s.db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		loaded := make([]witness.Receipt, 0, len(s.receipts))
		for _, receipt := range s.receipts {
			loaded = append(loaded, receipt)
		}
		sort.Slice(loaded, func(i, j int) bool { return loaded[i].ChainIndex < loaded[j].ChainIndex })
		return loaded, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT chain_index, head, witnessed_at, COALESCE(signature, ''), COALESCE(key_id, '')
FROM promtact_witness_receipts
ORDER BY chain_index`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var loaded []witness.Receipt
	for rows.Next() {
		var receipt witness.Receipt
		if err := rows.Scan(&receipt.ChainIndex, &receipt.Head, &receipt.WitnessedAt,
			&receipt.Signature, &receipt.KeyID); err != nil {
			return nil, err
		}
		loaded = append(loaded, receipt)
	}
	return loaded, rows.Err()
}

// LatestWitnessReceipt returns the highest-indexed receipt, if any.
func (s *Store) LatestWitnessReceipt() (witness.Receipt, bool) {
	receipts, err := s.WitnessReceipts()
	if err != nil || len(receipts) == 0 {
		return witness.Receipt{}, false
	}
	return receipts[len(receipts)-1], true
}
