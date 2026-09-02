// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"github.com/powfulf/Nusa/internal/ledger"
)

// The wire shapes.
//
// Domain types are embedded rather than redeclared wherever one exists.
// ledger.Money, ledger.Date and ledger.Rate carry their own MarshalJSON, and
// that is where §4.5 is enforced — a DTO with its own string field for an
// amount would be a second place where the wire shape of money is decided, and
// the second place is the one that eventually disagrees.
//
// Identities arrive from outside on the way in, which is unusual for REST and
// is §5.7 rather than a preference: nothing in this system mints an identity
// for something a caller can point at. So a create request carries its own id.

// listEnvelope is the shape of every collection response.
//
// Every listing uses it, including the ones that are not paginated and return
// their whole set — accounts and commodities. Their next_cursor is always null
// and they accept no cursor parameter, which is honest about today and leaves
// the door open: a client written against this envelope needs no special case
// if one of them ever grows a second page.
type listEnvelope struct {
	Items any `json:"items"`

	// NextCursor is the position the following page starts at, or null when
	// there is none. It is opaque: clients pass it back and never read it.
	NextCursor *string `json:"next_cursor"`
}

// commodityDTO describes a commodity.
type commodityDTO struct {
	Code  string `json:"code"`
	Kind  string `json:"kind"`
	Scale uint8  `json:"scale"`
}

func commodityToDTO(c ledger.Commodity) commodityDTO {
	return commodityDTO{Code: string(c.Code()), Kind: c.Kind().String(), Scale: c.Scale()}
}

// accountDTO describes an account.
type accountDTO struct {
	ID string `json:"id"`

	// ParentID is null for a root account rather than an empty string. The
	// domain uses "" for absent because a zero value has to mean something;
	// on the wire, null is what absent looks like, and a client checking for
	// "" would be reading a Go convention that has no reason to travel.
	ParentID *string `json:"parent_id"`

	Kind string `json:"kind"`
	Name string `json:"name"`

	// Commodity is null when the account may hold several, which is normal for
	// a brokerage account holding cash and shares at once.
	Commodity *string `json:"commodity"`

	Closed bool `json:"closed"`
}

func accountToDTO(a ledger.Account) accountDTO {
	return accountDTO{
		ID:        string(a.ID()),
		ParentID:  optional(string(a.Parent())),
		Kind:      a.Kind().String(),
		Name:      a.Name(),
		Commodity: optional(string(a.Commodity())),
		Closed:    a.IsClosed(),
	}
}

// postingDTO describes one line of a transaction.
type postingDTO struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id"`

	// Amount nests, so this reads as amount.amount on the wire. That is ugly
	// and it is deliberate: §4.5 fixes the shape of a money value as
	// {amount, commodity}, and ledger.Money is what enforces it. Naming this
	// field "value" to avoid the repetition would introduce a second
	// vocabulary for the thing the domain calls Amount, and a second
	// vocabulary is a thing to keep in sync forever. One ugly name beats two
	// tidy ones.
	Amount ledger.Money `json:"amount"`

	// Rate is null for a posting in the book's own currency. It is recorded
	// per posting at the moment of the transaction (§5.4), never looked up
	// when a report runs.
	Rate *ledger.Rate `json:"rate"`

	Memo string `json:"memo"`

	// ReversesID names the line this one undoes, on a reversing transaction.
	ReversesID *string `json:"reverses_id"`
}

// transactionDTO describes a transaction and its lines.
type transactionDTO struct {
	ID string `json:"id"`

	// Date is the civil date the transaction belongs to, and the only field
	// any calculation reads when deciding when something happened.
	Date ledger.Date `json:"date"`

	// OccurredAt and Timezone are display only. They are null when nothing
	// knew them, which is the common case for a manually entered transaction.
	OccurredAt *string `json:"occurred_at"`
	Timezone   *string `json:"timezone"`

	Payee string `json:"payee"`
	Memo  string `json:"memo"`

	// ReversesID and ReversalKind travel together or not at all, exactly as
	// they do in the domain and in the schema.
	ReversesID   *string `json:"reverses_id"`
	ReversalKind *string `json:"reversal_kind"`

	Postings []postingDTO `json:"postings"`
}

func transactionToDTO(t ledger.Transaction) transactionDTO {
	postings := t.Postings()
	lines := make([]postingDTO, 0, len(postings))
	for _, p := range postings {
		line := postingDTO{
			ID:         string(p.ID()),
			AccountID:  string(p.Account()),
			Amount:     p.Amount(),
			Memo:       p.Memo(),
			ReversesID: optional(string(p.Reverses())),
		}
		if rate := p.Rate(); !rate.IsZero() {
			line.Rate = &rate
		}
		lines = append(lines, line)
	}

	dto := transactionDTO{
		ID:         string(t.ID()),
		Date:       t.Date(),
		Payee:      t.Payee(),
		Memo:       t.Memo(),
		Timezone:   optional(t.Timezone()),
		ReversesID: optional(string(t.Reverses())),
		Postings:   lines,
	}
	if occurred := t.OccurredAt(); !occurred.IsZero() {
		formatted := occurred.UTC().Format(rfc3339Nano)
		dto.OccurredAt = &formatted
	}
	if t.IsReversal() {
		kind := t.ReversalKind().String()
		dto.ReversalKind = &kind
	}
	return dto
}

// rfc3339Nano is the instant format, spelled here rather than taken from the
// time package so that a change to it is a deliberate change to the API.
const rfc3339Nano = "2006-01-02T15:04:05.999999999Z07:00"

// optional turns the domain's "" for absent into the wire's null.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
