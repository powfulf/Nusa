// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/powfulf/Nusa/internal/ledger"
)

// DisposalResult reports what a disposal did.
//
// Consumed is keyed by the disposing line, because a transaction may sell two
// holdings at once and the two answers must not be merged. It is filled on a
// replay too, read back from what the first attempt wrote: a caller retrying a
// request that timed out needs the same answer, not an empty one.
type DisposalResult struct {
	Result

	// Consumed lists what each disposing line drew on, oldest lot first.
	Consumed map[ledger.PostingID][]ledger.Consumption
}

// SaveDisposal writes a transaction that disposes of part of a holding, and
// records which lots paid for it.
//
// The named postings are the lines that reduce a holding. Everything else
// about them is derived rather than supplied: the account is the line's
// account, and the quantity is the line's own amount negated. Passing the
// quantity separately would create a second place for it to be stated and
// therefore a way for the two to disagree — the line says how many units left
// the account, and that is the disposal.
//
// Selection is FIFO, which is the only policy in core. Which policies a
// taxpayer may use is a country-specific question, so the alternatives belong
// in Country Packs (§1).
//
// Everything happens in one database transaction: the header, the lines, the
// reduced lot figures and the consumption rows. A disposal that recorded the
// sale but not what it consumed would leave a holding whose remaining quantity
// no longer matched its history, and no error anywhere to say so.
func (s *Store) SaveDisposal(
	ctx context.Context, w Write, txn ledger.Transaction, disposing ...ledger.PostingID,
) (DisposalResult, error) {
	if err := w.validate(); err != nil {
		return DisposalResult{}, err
	}
	if !txn.IsValid() {
		return DisposalResult{}, fmt.Errorf("%w: transaction did not come from ledger.NewTransaction", ErrInvalidWrite)
	}
	if err := validateTransactionText(txn); err != nil {
		return DisposalResult{}, err
	}
	if len(disposing) == 0 {
		return DisposalResult{}, fmt.Errorf("%w: a disposal must name at least one disposing line",
			ErrInvalidWrite)
	}

	lines, err := disposingLines(txn, disposing)
	if err != nil {
		return DisposalResult{}, err
	}

	tree, err := s.LoadAccountTree(ctx)
	if err != nil {
		return DisposalResult{}, err
	}
	if _, err := ledger.NewJournal(tree, txn); err != nil {
		return DisposalResult{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DisposalResult{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.WithTx(tx)

	claim, err := claimKey(ctx, q, w, "transaction", string(txn.ID()), fingerprintDisposal(txn, disposing))
	if err != nil {
		return DisposalResult{}, err
	}
	if claim.replayed {
		// The write already happened. Read back what it consumed rather than
		// answering with nothing: a caller retrying after a timeout is asking
		// the same question and is owed the same answer.
		consumed, err := readConsumptions(ctx, q, lines)
		if err != nil {
			return DisposalResult{}, err
		}
		return DisposalResult{
			Result:   Result{EntityID: claim.entityID, Replayed: true},
			Consumed: consumed,
		}, nil
	}

	if err := writeTransactionRow(ctx, q, txn); err != nil {
		return DisposalResult{}, err
	}

	consumed := make(map[ledger.PostingID][]ledger.Consumption, len(lines))
	for _, line := range lines {
		consumptions, err := consumeForLine(ctx, q, line)
		if err != nil {
			return DisposalResult{}, err
		}
		consumed[line.posting] = consumptions
	}

	diff, err := disposalDiff(txn, consumed)
	if err != nil {
		return DisposalResult{}, err
	}
	if err := writeAudit(ctx, q, w, disposalAuditAction(txn), "transaction", string(txn.ID()), diff); err != nil {
		return DisposalResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		if unbalancedError(err) {
			return DisposalResult{}, fmt.Errorf("%w: %w", ErrUnbalanced, err)
		}
		return DisposalResult{}, fmt.Errorf("commit: %w", err)
	}
	return DisposalResult{Result: Result{EntityID: string(txn.ID())}, Consumed: consumed}, nil
}

// disposingLine is one line that reduces a holding, with the quantity read off
// the line itself.
type disposingLine struct {
	posting  ledger.PostingID
	account  ledger.AccountID
	quantity ledger.Money
}

func disposingLines(txn ledger.Transaction, named []ledger.PostingID) ([]disposingLine, error) {
	byID := make(map[ledger.PostingID]ledger.Posting, len(txn.Postings()))
	for _, p := range txn.Postings() {
		byID[p.ID()] = p
	}

	seen := make(map[ledger.PostingID]struct{}, len(named))
	out := make([]disposingLine, 0, len(named))
	for _, id := range named {
		posting, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("%w: posting %s is not a line of transaction %s",
				ErrInvalidWrite, id, txn.ID())
		}
		if _, twice := seen[id]; twice {
			return nil, fmt.Errorf("%w: posting %s is named as disposing twice",
				ErrInvalidWrite, id)
		}
		seen[id] = struct{}{}

		// A disposal takes units out of an account, so the line is negative.
		// A positive line is an acquisition, and consuming lots against one
		// would reduce a holding that just grew.
		if posting.Amount().Sign() >= 0 {
			return nil, fmt.Errorf("%w: posting %s holds %s, which does not dispose of anything",
				ErrInvalidWrite, id, posting.Amount())
		}
		quantity, err := posting.Amount().Neg()
		if err != nil {
			return nil, err
		}
		out = append(out, disposingLine{posting: id, account: posting.Account(), quantity: quantity})
	}
	return out, nil
}

// consumeForLine selects the lots one line draws on and writes both halves of
// the result: the reduced running figure, and the appended record of what
// reduced it.
func consumeForLine(ctx context.Context, q *Queries, line disposingLine) ([]ledger.Consumption, error) {
	account, err := uuidFrom(string(line.account))
	if err != nil {
		return nil, err
	}

	// Locked for the length of this database transaction, so a second disposal
	// on the same account cannot read the same lots and consume them again.
	rows, err := q.ListOpenLotsByAccountForUpdate(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("list open lots of %s: %w", line.account, err)
	}

	open := make([]ledger.Lot, 0, len(rows))
	for _, row := range rows {
		lot, err := lotTo(row)
		if err != nil {
			return nil, err
		}
		// A holding can contain lots of several commodities when the account
		// is unrestricted, and only the ones being disposed of are relevant.
		if lot.Quantity().Commodity() != line.quantity.Commodity() {
			continue
		}
		open = append(open, lot)
	}
	if len(open) == 0 {
		return nil, fmt.Errorf("%w: account %s holds no open lots of %s to dispose of",
			ledger.ErrInsufficientLots, line.account, line.quantity.Commodity())
	}

	reduced, consumptions, err := ledger.ConsumeFIFO(open, line.quantity)
	if err != nil {
		return nil, fmt.Errorf("dispose of %s from %s: %w", line.quantity, line.account, err)
	}

	// Only the lots that actually changed are written back. ConsumeFIFO
	// returns every lot it was given, touched or not.
	touched := make(map[ledger.LotID]struct{}, len(consumptions))
	for _, c := range consumptions {
		touched[c.Lot] = struct{}{}
	}
	for _, lot := range reduced {
		if _, changed := touched[lot.ID()]; !changed {
			continue
		}
		if err := writeLotRemaining(ctx, q, lot); err != nil {
			return nil, err
		}
	}

	posting, err := uuidFrom(string(line.posting))
	if err != nil {
		return nil, err
	}
	for _, c := range consumptions {
		if err := writeConsumption(ctx, q, posting, c); err != nil {
			return nil, err
		}
	}
	return consumptions, nil
}

func writeLotRemaining(ctx context.Context, q *Queries, lot ledger.Lot) error {
	id, err := uuidFrom(string(lot.ID()))
	if err != nil {
		return err
	}
	if err := q.SetLotRemaining(ctx, SetLotRemainingParams{
		ID:              id,
		RemainingAmount: numericFrom(lot.Remaining().Amount()),
	}); err != nil {
		return fmt.Errorf("reduce lot %s: %w", lot.ID(), err)
	}
	return nil
}

func writeConsumption(ctx context.Context, q *Queries, posting pgtype.UUID, c ledger.Consumption) error {
	lot, err := uuidFrom(string(c.Lot))
	if err != nil {
		return err
	}
	quantity, quantityCommodity, err := moneyFrom(c.Quantity)
	if err != nil {
		return fmt.Errorf("consumption of lot %s: %w", c.Lot, err)
	}
	if !c.Basis.IsValid() {
		return fmt.Errorf("%w: consumption of lot %s has no cost basis", ErrInvalidWrite, c.Lot)
	}

	// The basis is exact and stays exact. big.Rat is already in lowest terms
	// with a positive denominator, which is the form the column constraints
	// require, so nothing is normalised here — and nothing is rounded, which
	// is the whole reason these are two columns (§4.6, §4.7).
	value := c.Basis.Value()

	if err := q.InsertLotConsumption(ctx, InsertLotConsumptionParams{
		PostingID:         posting,
		LotID:             lot,
		QuantityAmount:    quantity,
		QuantityCommodity: quantityCommodity,
		BasisNum:          numericFrom(value.Num()),
		BasisDen:          numericFrom(value.Denom()),
		BasisCommodity:    string(c.Basis.Commodity()),
	}); err != nil {
		return fmt.Errorf("record consumption of lot %s: %w", c.Lot, err)
	}
	return nil
}

// Consumptions returns what one disposing line drew on, oldest lot first.
//
// This is the answer to "why is the realised gain this number", which §7 says
// a person is owed when they sell something. It is read rather than
// recomputed: re-running FIFO over today's lots does not reproduce a selection
// made when a different set of lots was open.
func (s *Store) Consumptions(ctx context.Context, posting ledger.PostingID) ([]ledger.Consumption, error) {
	id, err := uuidFrom(string(posting))
	if err != nil {
		return nil, err
	}
	rows, err := s.ListConsumptionsByPosting(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list consumptions of %s: %w", posting, err)
	}
	return consumptionsTo(rows)
}

// ConsumedFromLot returns everything that has ever drawn on one lot.
//
// Summing these against the lot's original quantity reconstructs its remaining
// figure from the appended record, which is the drift check §5.2 asks for
// wherever a running number exists.
func (s *Store) ConsumedFromLot(ctx context.Context, lot ledger.LotID) ([]ledger.Consumption, error) {
	id, err := uuidFrom(string(lot))
	if err != nil {
		return nil, err
	}
	rows, err := s.ListConsumptionsByLot(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list consumptions of lot %s: %w", lot, err)
	}
	return consumptionsTo(rows)
}

func readConsumptions(
	ctx context.Context, q *Queries, lines []disposingLine,
) (map[ledger.PostingID][]ledger.Consumption, error) {
	out := make(map[ledger.PostingID][]ledger.Consumption, len(lines))
	for _, line := range lines {
		id, err := uuidFrom(string(line.posting))
		if err != nil {
			return nil, err
		}
		rows, err := q.ListConsumptionsByPosting(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("list consumptions of %s: %w", line.posting, err)
		}
		consumptions, err := consumptionsTo(rows)
		if err != nil {
			return nil, err
		}
		out[line.posting] = consumptions
	}
	return out, nil
}

func consumptionsTo(rows []LotConsumption) ([]ledger.Consumption, error) {
	out := make([]ledger.Consumption, 0, len(rows))
	for _, row := range rows {
		quantity, err := moneyTo(row.QuantityAmount, row.QuantityCommodity)
		if err != nil {
			return nil, fmt.Errorf("consumption of lot %s: %w", uuidTo(row.LotID), err)
		}
		num, err := numericTo(row.BasisNum)
		if err != nil {
			return nil, fmt.Errorf("consumption of lot %s basis: %w", uuidTo(row.LotID), err)
		}
		den, err := numericTo(row.BasisDen)
		if err != nil {
			return nil, fmt.Errorf("consumption of lot %s basis: %w", uuidTo(row.LotID), err)
		}
		if den.Sign() <= 0 {
			return nil, fmt.Errorf("%w: consumption of lot %s has denominator %s",
				ErrCorrupt, uuidTo(row.LotID), den)
		}
		basis, err := ledger.NewRat(ledger.CommodityCode(row.BasisCommodity), new(big.Rat).SetFrac(num, den))
		if err != nil {
			return nil, fmt.Errorf("%w: consumption of lot %s: %w", ErrCorrupt, uuidTo(row.LotID), err)
		}
		out = append(out, ledger.Consumption{
			Lot:      ledger.LotID(uuidTo(row.LotID)),
			Quantity: quantity,
			Basis:    basis,
		})
	}
	return out, nil
}

// lotTo turns a lot row into a domain value.
func lotTo(row Lot) (ledger.Lot, error) {
	id := uuidTo(row.ID)

	quantity, err := moneyTo(row.QuantityAmount, row.QuantityCommodity)
	if err != nil {
		return ledger.Lot{}, fmt.Errorf("lot %s quantity: %w", id, err)
	}
	remaining, err := moneyTo(row.RemainingAmount, row.RemainingCommodity)
	if err != nil {
		return ledger.Lot{}, fmt.Errorf("lot %s remaining: %w", id, err)
	}
	cost, err := moneyTo(row.CostAmount, row.CostCommodity)
	if err != nil {
		return ledger.Lot{}, fmt.Errorf("lot %s cost: %w", id, err)
	}
	openedOn, err := dateTo(row.OpenedOn)
	if err != nil {
		return ledger.Lot{}, fmt.Errorf("lot %s: %w", id, err)
	}

	lot, err := ledger.NewLot(ledger.LotSpec{
		ID:        ledger.LotID(id),
		Account:   ledger.AccountID(uuidTo(row.AccountID)),
		OpenedBy:  ledger.PostingID(uuidTo(row.OpenedBy)),
		OpenedOn:  openedOn,
		Quantity:  quantity,
		Cost:      cost,
		Remaining: &remaining,
	})
	if err != nil {
		return ledger.Lot{}, fmt.Errorf("%w: lot %s: %w", ErrCorrupt, id, err)
	}
	return lot, nil
}

// disposalAuditAction names what a disposal did.
//
// A disposal that is also a reversal keeps the reversal's action, because what
// matters about it is that it undid something. Everything else is a disposal
// in its own right and is named as one — the log should not describe a sale as
// a "create" when the whole point of the entry is that a holding went down.
func disposalAuditAction(txn ledger.Transaction) string {
	if txn.IsReversal() {
		return auditAction(txn)
	}
	return "dispose"
}
