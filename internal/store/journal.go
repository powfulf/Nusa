// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/GaffaQ/Nusa/internal/ledger"
)

// SaveAccount writes one account.
//
// The tree rules — no missing parent, no cycle, no child of another kind — are
// checked by ledger.NewAccountTree against the accounts already stored, before
// anything is written. The database cannot express them, and this is the layer
// that knows both the candidate and the tree it is joining.
func (s *Store) SaveAccount(ctx context.Context, account ledger.Account) error {
	if err := nulNotAllowed(fmt.Sprintf("account %s name", account.ID()), account.Name()); err != nil {
		return err
	}

	existing, err := s.LoadAccounts(ctx)
	if err != nil {
		return err
	}
	if _, err := ledger.NewAccountTree(append(existing, account)...); err != nil {
		return err
	}

	id, err := uuidFrom(string(account.ID()))
	if err != nil {
		return err
	}
	parent, err := uuidFrom(string(account.Parent()))
	if err != nil {
		return err
	}

	return s.InsertAccount(ctx, InsertAccountParams{
		ID:            id,
		ParentID:      parent,
		Kind:          account.Kind().String(),
		Name:          account.Name(),
		CommodityCode: textFrom(string(account.Commodity())),
		Closed:        account.IsClosed(),
	})
}

// LoadAccounts reads every account as a domain value.
func (s *Store) LoadAccounts(ctx context.Context) ([]ledger.Account, error) {
	rows, err := s.ListAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}

	accounts := make([]ledger.Account, 0, len(rows))
	for _, row := range rows {
		kind, err := accountKind(row.Kind)
		if err != nil {
			return nil, err
		}
		account, err := ledger.NewAccount(ledger.AccountSpec{
			ID:        ledger.AccountID(uuidTo(row.ID)),
			Parent:    ledger.AccountID(uuidTo(row.ParentID)),
			Kind:      kind,
			Name:      row.Name,
			Commodity: ledger.CommodityCode(textTo(row.CommodityCode)),
			Closed:    row.Closed,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: account %s: %w", ErrCorrupt, uuidTo(row.ID), err)
		}
		accounts = append(accounts, account)
	}
	return accounts, nil
}

// LoadAccountTree reads every account and validates the tree they form.
//
// The whole set is read rather than the ancestors of one account, because
// NewAccountTree refuses a parent it cannot see, and a household book is tens
// to low hundreds of rows. Reading all of them is both simpler and honest
// about what the domain needs.
func (s *Store) LoadAccountTree(ctx context.Context) (*ledger.AccountTree, error) {
	accounts, err := s.LoadAccounts(ctx)
	if err != nil {
		return nil, err
	}
	tree, err := ledger.NewAccountTree(accounts...)
	if err != nil {
		return nil, fmt.Errorf("%w: stored accounts do not form a tree: %w", ErrCorrupt, err)
	}
	return tree, nil
}

func accountKind(name string) (ledger.AccountKind, error) {
	for _, k := range []ledger.AccountKind{
		ledger.AccountAsset, ledger.AccountLiability, ledger.AccountEquity,
		ledger.AccountIncome, ledger.AccountExpense,
	} {
		if k.String() == name {
			return k, nil
		}
	}
	return ledger.AccountUnknown, fmt.Errorf("%w: unknown account kind %q", ErrCorrupt, name)
}

// SaveTransaction writes a transaction, its postings and any lots it opens, in
// one database transaction.
//
// The order is deliberate and is the whole of point 3 of the milestone: the
// domain validates first, against the stored account tree, and only then does
// anything reach the database. The deferred constraint trigger checks the
// balance again at COMMIT, which is not redundancy — it is the guard that
// still holds when a future importer or rule engine writes without coming
// through here.
//
// Replaying a write with the same key and the same request writes nothing and
// reports Replayed.
func (s *Store) SaveTransaction(
	ctx context.Context, w Write, txn ledger.Transaction, lots ...ledger.Lot,
) (Result, error) {
	if err := w.validate(); err != nil {
		return Result{}, err
	}
	if !txn.IsValid() {
		return Result{}, fmt.Errorf("%w: transaction did not come from ledger.NewTransaction", ErrInvalidWrite)
	}
	if err := validateTransactionText(txn); err != nil {
		return Result{}, err
	}

	// Validation happens in the domain, before the write, exactly as the
	// milestone requires. NewJournal is the door: it checks that every posting
	// lands in an account that exists and accepts that commodity, and the
	// balance was already proved by NewTransaction. The journal itself is
	// discarded — it was the validator, not a value we need.
	tree, err := s.LoadAccountTree(ctx)
	if err != nil {
		return Result{}, err
	}
	if _, err := ledger.NewJournal(tree, txn); err != nil {
		return Result{}, err
	}
	for _, lot := range lots {
		if _, err := tree.Require(lot.Account()); err != nil {
			return Result{}, fmt.Errorf("lot %s: %w", lot.ID(), err)
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.WithTx(tx)

	claim, err := claimKey(ctx, q, w, "transaction", string(txn.ID()), fingerprintTransaction(txn))
	if err != nil {
		return Result{}, err
	}
	if claim.replayed {
		// Nothing was written, so there is nothing to commit. Rolling back is
		// not a failure here; it is the correct end to a write that had
		// already happened.
		return Result{EntityID: claim.entityID, Replayed: true}, nil
	}

	if err := writeTransactionRow(ctx, q, txn); err != nil {
		return Result{}, err
	}
	for _, lot := range lots {
		if err := writeLotRow(ctx, q, lot); err != nil {
			return Result{}, err
		}
	}

	diff, err := transactionDiff(txn)
	if err != nil {
		return Result{}, err
	}
	if err := writeAudit(ctx, q, w, "create", "transaction", string(txn.ID()), diff); err != nil {
		return Result{}, err
	}

	// The deferred balance trigger fires here, not before.
	if err := tx.Commit(ctx); err != nil {
		if unbalancedError(err) {
			return Result{}, fmt.Errorf("%w: %w", ErrUnbalanced, err)
		}
		return Result{}, fmt.Errorf("commit: %w", err)
	}
	return Result{EntityID: string(txn.ID())}, nil
}

// maxPostingsPerTransaction is what the ordinal column can count to. The
// ordinal is a smallint and zero-based, so 32768 lines is the ceiling.
//
// No real transaction comes close. The check is here because the alternative
// is a silent wrap to a negative ordinal, which the ordinal >= 0 constraint
// would then refuse with an error naming neither the cause nor the fix.
const maxPostingsPerTransaction = 32768

func writeTransactionRow(ctx context.Context, q *Queries, txn ledger.Transaction) error {
	postings := txn.Postings()
	if len(postings) > maxPostingsPerTransaction {
		return fmt.Errorf("%w: transaction %s has %d postings, more than the %d an ordinal can number",
			ErrInvalidWrite, txn.ID(), len(postings), maxPostingsPerTransaction)
	}

	id, err := uuidFrom(string(txn.ID()))
	if err != nil {
		return err
	}

	if err := q.InsertTransaction(ctx, InsertTransactionParams{
		ID:         id,
		TxnDate:    dateFrom(txn.Date()),
		OccurredAt: timestampFrom(txn.OccurredAt()),
		Timezone:   txn.Timezone(),
		Payee:      txn.Payee(),
		Memo:       txn.Memo(),
	}); err != nil {
		return fmt.Errorf("insert transaction %s: %w", txn.ID(), err)
	}

	for ordinal, p := range postings {
		if err := writePostingRow(ctx, q, txn, int16(ordinal), p); err != nil {
			return err
		}
	}
	return nil
}

func writePostingRow(ctx context.Context, q *Queries, txn ledger.Transaction, ordinal int16, p ledger.Posting) error {
	id, err := uuidFrom(string(p.ID()))
	if err != nil {
		return err
	}
	transactionID, err := uuidFrom(string(txn.ID()))
	if err != nil {
		return err
	}
	accountID, err := uuidFrom(string(p.Account()))
	if err != nil {
		return err
	}
	amount, commodity, err := moneyFrom(p.Amount())
	if err != nil {
		return fmt.Errorf("posting %s: %w", p.ID(), err)
	}
	rate := rateFrom(p.Rate())

	// The ordinal is the position the author wrote, carried so the transaction
	// reads back the way it was written. It is never used to identify a
	// posting; that is what PostingID is for.
	if err := q.InsertPosting(ctx, InsertPostingParams{
		ID:            id,
		TransactionID: transactionID,
		TxnDate:       dateFrom(txn.Date()),
		Ordinal:       ordinal,
		AccountID:     accountID,
		Amount:        amount,
		CommodityCode: commodity,
		RateBase:      rate.base,
		RateQuote:     rate.quote,
		RateNum:       rate.num,
		RateDen:       rate.den,
		Memo:          p.Memo(),
	}); err != nil {
		return fmt.Errorf("insert posting %s: %w", p.ID(), err)
	}
	return nil
}

func writeLotRow(ctx context.Context, q *Queries, lot ledger.Lot) error {
	id, err := uuidFrom(string(lot.ID()))
	if err != nil {
		return err
	}
	accountID, err := uuidFrom(string(lot.Account()))
	if err != nil {
		return err
	}
	openedBy, err := uuidFrom(string(lot.OpenedBy()))
	if err != nil {
		return err
	}
	quantity, quantityCommodity, err := moneyFrom(lot.Quantity())
	if err != nil {
		return fmt.Errorf("lot %s quantity: %w", lot.ID(), err)
	}
	remaining, remainingCommodity, err := moneyFrom(lot.Remaining())
	if err != nil {
		return fmt.Errorf("lot %s remaining: %w", lot.ID(), err)
	}
	cost, costCommodity, err := moneyFrom(lot.Cost())
	if err != nil {
		return fmt.Errorf("lot %s cost: %w", lot.ID(), err)
	}

	if err := q.InsertLot(ctx, InsertLotParams{
		ID:                 id,
		AccountID:          accountID,
		OpenedBy:           openedBy,
		OpenedOn:           dateFrom(lot.OpenedOn()),
		QuantityAmount:     quantity,
		QuantityCommodity:  quantityCommodity,
		RemainingAmount:    remaining,
		RemainingCommodity: remainingCommodity,
		CostAmount:         cost,
		CostCommodity:      costCommodity,
	}); err != nil {
		return fmt.Errorf("insert lot %s: %w", lot.ID(), err)
	}
	return nil
}

// LoadTransaction reads one transaction back as a domain value, postings in
// the order they were written.
func (s *Store) LoadTransaction(ctx context.Context, id ledger.TransactionID) (ledger.Transaction, error) {
	key, err := uuidFrom(string(id))
	if err != nil {
		return ledger.Transaction{}, err
	}

	row, err := s.GetTransaction(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ledger.Transaction{}, fmt.Errorf("%w: transaction %s", ErrNotFound, id)
		}
		return ledger.Transaction{}, fmt.Errorf("get transaction %s: %w", id, err)
	}

	postingRows, err := s.ListPostingsByTransaction(ctx, key)
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("list postings of %s: %w", id, err)
	}

	postings := make([]ledger.Posting, 0, len(postingRows))
	for _, pr := range postingRows {
		amount, err := moneyTo(pr.Amount, pr.CommodityCode)
		if err != nil {
			return ledger.Transaction{}, fmt.Errorf("posting %s: %w", uuidTo(pr.ID), err)
		}
		rate, err := rateTo(rateColumns{
			base: pr.RateBase, quote: pr.RateQuote, num: pr.RateNum, den: pr.RateDen,
		})
		if err != nil {
			return ledger.Transaction{}, fmt.Errorf("posting %s: %w", uuidTo(pr.ID), err)
		}
		posting, err := ledger.NewPosting(ledger.PostingSpec{
			ID:      ledger.PostingID(uuidTo(pr.ID)),
			Account: ledger.AccountID(uuidTo(pr.AccountID)),
			Amount:  amount,
			Rate:    rate,
			Memo:    pr.Memo,
		})
		if err != nil {
			return ledger.Transaction{}, fmt.Errorf("%w: posting %s: %w", ErrCorrupt, uuidTo(pr.ID), err)
		}
		postings = append(postings, posting)
	}

	date, err := dateTo(row.TxnDate)
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("transaction %s: %w", id, err)
	}

	txn, err := ledger.NewTransaction(ledger.TransactionSpec{
		ID:         ledger.TransactionID(uuidTo(row.ID)),
		Date:       date,
		OccurredAt: timestampTo(row.OccurredAt),
		Timezone:   row.Timezone,
		Payee:      row.Payee,
		Memo:       row.Memo,
		Postings:   postings,
	})
	if err != nil {
		return ledger.Transaction{}, fmt.Errorf("%w: transaction %s: %w", ErrCorrupt, id, err)
	}
	return txn, nil
}

// transactionDiff renders what was written, for the audit log.
//
// Postings are immutable, so for the ledger an audit entry records what was
// created rather than a before and an after. Money and Rate carry their own
// exact JSON encodings, so the amounts in the log are the amounts that were
// stored and not a rendering of them.
func transactionDiff(txn ledger.Transaction) ([]byte, error) {
	type postingDiff struct {
		ID      string       `json:"id"`
		Account string       `json:"account"`
		Amount  ledger.Money `json:"amount"`
		Rate    *ledger.Rate `json:"rate,omitempty"`
		Memo    string       `json:"memo,omitempty"`
	}
	type diff struct {
		ID       string        `json:"id"`
		Date     ledger.Date   `json:"date"`
		Payee    string        `json:"payee,omitempty"`
		Memo     string        `json:"memo,omitempty"`
		Postings []postingDiff `json:"postings"`
	}

	out := diff{
		ID:       string(txn.ID()),
		Date:     txn.Date(),
		Payee:    txn.Payee(),
		Memo:     txn.Memo(),
		Postings: make([]postingDiff, 0, len(txn.Postings())),
	}
	for _, p := range txn.Postings() {
		entry := postingDiff{
			ID:      string(p.ID()),
			Account: string(p.Account()),
			Amount:  p.Amount(),
			Memo:    p.Memo(),
		}
		if rate := p.Rate(); !rate.IsZero() {
			entry.Rate = &rate
		}
		out.Postings = append(out.Postings, entry)
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(out); err != nil {
		return nil, fmt.Errorf("encode audit diff for %s: %w", txn.ID(), err)
	}
	return buf.Bytes(), nil
}

// LoadLot reads one lot back as a domain value.
//
// The lot names the posting that opened it, never the transaction: a
// transaction can acquire two things at once, and "which line was this lot"
// would then have no answer. The owning transaction stays recoverable through
// GetPostingOwner, so the narrower fact loses nothing.
func (s *Store) LoadLot(ctx context.Context, id ledger.LotID) (ledger.Lot, error) {
	key, err := uuidFrom(string(id))
	if err != nil {
		return ledger.Lot{}, err
	}

	row, err := s.GetLot(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ledger.Lot{}, fmt.Errorf("%w: lot %s", ErrNotFound, id)
		}
		return ledger.Lot{}, fmt.Errorf("get lot %s: %w", id, err)
	}

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
		ID:        ledger.LotID(uuidTo(row.ID)),
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

// PostingOwner widens a posting back to the transaction that contains it.
func (s *Store) PostingOwner(ctx context.Context, id ledger.PostingID) (ledger.TransactionID, error) {
	key, err := uuidFrom(string(id))
	if err != nil {
		return "", err
	}
	owner, err := s.GetPostingOwner(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("%w: posting %s", ErrNotFound, id)
		}
		return "", fmt.Errorf("posting owner of %s: %w", id, err)
	}
	return ledger.TransactionID(uuidTo(owner)), nil
}
