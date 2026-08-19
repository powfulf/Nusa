// SPDX-License-Identifier: MIT

package ledger

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// TransactionID identifies a transaction. Like AccountID it is a distinct
// type, and like AccountID it is never generated here — the client supplies a
// UUIDv7 so that replaying a write cannot duplicate it (§5.6).
type TransactionID string

// PostingID identifies a single posting.
//
// Postings need their own identity because things point at them: a lot records
// which posting opened it, an audit entry records which posting a rule wrote,
// and a reversing entry records which posting it undoes. Identifying a posting
// by its position in a transaction would make all of those break the moment
// anything is reordered.
type PostingID string

// PostingSpec is the input to NewPosting.
type PostingSpec struct {
	// ID identifies the posting. Required, and supplied by the caller.
	ID PostingID

	// Account is where the value lands. Required.
	Account AccountID

	// Amount is signed: positive is a debit, negative is a credit. Required.
	Amount Money

	// Rate is the exchange rate for this posting's commodity at the moment of
	// the transaction, and it prices this posting's own commodity — its base
	// must be Amount's commodity. Optional; a posting in the book's own
	// currency needs none.
	//
	// It is recorded here, per posting, rather than looked up when a report
	// runs, because §5.4 requires last year's report to say the same thing
	// today as it did last year.
	Rate Rate

	// Memo is the user's note about this line. User data, never translated.
	Memo string
}

// Posting is one line of a transaction: an amount landing in one account.
//
// Postings are immutable (§5.3). There is no setter, every field is
// unexported, and the Money inside copies its amount on the way out, so a
// posting cannot be changed by anyone holding it — not even through the
// pointer inside its amount. A correction is a new, reversing posting; a
// deletion is a tombstone. History is append-only because the type gives no
// other option.
type Posting struct {
	id      PostingID
	account AccountID
	amount  Money
	rate    Rate
	memo    string
}

// NewPosting validates a spec and returns the posting it describes.
func NewPosting(spec PostingSpec) (Posting, error) {
	if err := validateIDAs(ErrInvalidTransaction, "posting id", string(spec.ID)); err != nil {
		return Posting{}, err
	}
	if err := validateIDAs(ErrInvalidTransaction, "posting account id", string(spec.Account)); err != nil {
		return Posting{}, err
	}
	if err := spec.Amount.validate(); err != nil {
		return Posting{}, fmt.Errorf("posting %q: %w", spec.ID, err)
	}
	if !spec.Rate.IsZero() && spec.Rate.Base() != spec.Amount.Commodity() {
		return Posting{}, fmt.Errorf("%w: posting %q holds %s but its rate prices %s",
			ErrInvalidRate, spec.ID, spec.Amount.Commodity(), spec.Rate.Base())
	}
	return Posting{
		id:      spec.ID,
		account: spec.Account,
		amount:  spec.Amount,
		rate:    spec.Rate,
		memo:    spec.Memo,
	}, nil
}

// IsValid reports whether the posting came from NewPosting.
func (p Posting) IsValid() bool { return p.id != "" && p.account != "" && p.amount.IsValid() }

// ID returns the posting's identity.
func (p Posting) ID() PostingID { return p.id }

// Account returns where the value lands.
func (p Posting) Account() AccountID { return p.account }

// Amount returns the signed amount. Positive is a debit.
func (p Posting) Amount() Money { return p.amount }

// Rate returns the exchange rate captured with this posting, or the zero Rate
// if the posting needed no conversion.
func (p Posting) Rate() Rate { return p.rate }

// Memo returns the user's note about this line.
func (p Posting) Memo() string { return p.memo }

// String renders the posting for logs and test failures.
func (p Posting) String() string {
	if !p.IsValid() {
		return "<invalid posting>"
	}
	s := string(p.account) + " " + p.amount.String()
	if !p.rate.IsZero() {
		s += " @ " + p.rate.String()
	}
	return s
}

// TransactionSpec is the input to NewTransaction.
type TransactionSpec struct {
	// ID identifies the transaction. Required, and supplied by the caller.
	ID TransactionID

	// Date is the civil date the transaction belongs to. Required. This is
	// the only field any calculation reads when deciding when something
	// happened.
	Date Date

	// OccurredAt is the instant the transaction happened, if anything knows
	// it. Optional, and display only — see Date.
	OccurredAt time.Time

	// Timezone is the IANA zone OccurredAt was observed in, for rendering it
	// back the way the user saw it. Optional, and display only.
	//
	// It is stored as written and not checked against the zone database,
	// because resolving a zone name means reading tzdata off disk and this
	// package does no I/O. Whoever renders it validates it.
	Timezone string

	// Payee is who the money went to or came from. User data.
	Payee string

	// Memo is the user's note about the whole transaction. User data.
	Memo string

	// Postings are the lines. At least two, summing to zero per commodity.
	Postings []Posting
}

// Transaction is a set of postings that together move value without creating
// or destroying any.
//
// Constructing one runs the balance check, so an unbalanced Transaction value
// cannot exist. There is no separate "validate" step a caller might forget,
// and no window in which a half-built transaction is reachable.
type Transaction struct {
	id         TransactionID
	date       Date
	occurredAt time.Time
	timezone   string
	payee      string
	memo       string
	postings   []Posting
}

// NewTransaction validates a spec and returns the transaction it describes.
//
// The balance rule is absolute (§5.1): every commodity must total exactly
// zero, with no tolerance and no rounding allowance. A transaction that
// changes commodity balances by adding the conversion postings that make it
// true — explicitly, against an equity trading account, before it gets here.
// See AccountTree.ConversionPostings. Nothing is ever synthesised during
// validation, because a validator that quietly fixes its input is a validator
// that can hide an importer's bug for a year.
func NewTransaction(spec TransactionSpec) (Transaction, error) {
	if err := validateIDAs(ErrInvalidTransaction, "transaction id", string(spec.ID)); err != nil {
		return Transaction{}, err
	}
	if spec.Date.IsZero() {
		return Transaction{}, fmt.Errorf("%w: transaction %q has no date", ErrInvalidTransaction, spec.ID)
	}
	if len(spec.Postings) < 2 {
		return Transaction{}, fmt.Errorf("%w: transaction %q has %d postings, a transaction needs at least 2",
			ErrInvalidTransaction, spec.ID, len(spec.Postings))
	}

	seen := make(map[PostingID]struct{}, len(spec.Postings))
	for i, p := range spec.Postings {
		if !p.IsValid() {
			return Transaction{}, wrapPostingIndex(i, ErrInvalidTransaction, "unconstructed posting")
		}
		if _, exists := seen[p.id]; exists {
			return Transaction{}, fmt.Errorf("%w: transaction %q has two postings called %q",
				ErrDuplicateID, spec.ID, p.id)
		}
		seen[p.id] = struct{}{}
	}

	sums, err := SumPostings(spec.Postings)
	if err != nil {
		return Transaction{}, fmt.Errorf("transaction %q: %w", spec.ID, err)
	}
	if !sums.IsZero() {
		residual := make([]string, 0, sums.Len())
		for _, m := range sums.Nonzero() {
			residual = append(residual, m.String())
		}
		return Transaction{}, fmt.Errorf("%w: transaction %q is short by %s",
			ErrUnbalanced, spec.ID, strings.Join(residual, ", "))
	}

	return Transaction{
		id:         spec.ID,
		date:       spec.Date,
		occurredAt: spec.OccurredAt,
		timezone:   spec.Timezone,
		payee:      spec.Payee,
		memo:       spec.Memo,
		postings:   slices.Clone(spec.Postings),
	}, nil
}

// IsValid reports whether the transaction came from NewTransaction.
func (t Transaction) IsValid() bool { return t.id != "" && len(t.postings) >= 2 }

// ID returns the transaction's identity.
func (t Transaction) ID() TransactionID { return t.id }

// Date returns the civil date the transaction belongs to. This is the field
// every calculation reads.
func (t Transaction) Date() Date { return t.date }

// OccurredAt returns the instant the transaction happened, or the zero time if
// nothing recorded one. Display only — never sort, filter or bucket by it.
func (t Transaction) OccurredAt() time.Time { return t.occurredAt }

// Timezone returns the IANA zone OccurredAt was observed in. Display only.
func (t Transaction) Timezone() string { return t.timezone }

// Payee returns who the money went to or came from.
func (t Transaction) Payee() string { return t.payee }

// Memo returns the user's note about the transaction.
func (t Transaction) Memo() string { return t.memo }

// Postings returns the lines, in the order they were given. The slice is a
// copy, so appending to it or replacing an element changes nothing here.
func (t Transaction) Postings() []Posting { return slices.Clone(t.postings) }

// PostingsFor returns the lines that land in one account, in order.
func (t Transaction) PostingsFor(account AccountID) []Posting {
	out := make([]Posting, 0, len(t.postings))
	for _, p := range t.postings {
		if p.account == account {
			out = append(out, p)
		}
	}
	return out
}

// Commodities returns every commodity the transaction touches, in sorted
// order. More than one means a conversion happened.
func (t Transaction) Commodities() []CommodityCode {
	codes := make([]CommodityCode, 0, 2)
	for _, p := range t.postings {
		if !slices.Contains(codes, p.amount.commodity) {
			codes = append(codes, p.amount.commodity)
		}
	}
	slices.Sort(codes)
	return codes
}

// String renders the transaction for logs and test failures.
func (t Transaction) String() string {
	if !t.IsValid() {
		return "<invalid transaction>"
	}
	lines := make([]string, 0, len(t.postings))
	for _, p := range t.postings {
		lines = append(lines, p.String())
	}
	return fmt.Sprintf("%s %s [%s]", t.date, t.id, strings.Join(lines, "; "))
}

func wrapPostingIndex(index int, err error, detail string) error {
	if detail == "" {
		return fmt.Errorf("posting %d: %w", index, err)
	}
	return fmt.Errorf("posting %d: %w: %s", index, err, detail)
}
