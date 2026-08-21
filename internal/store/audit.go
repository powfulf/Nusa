// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"fmt"
	"time"
)

// Section 10: every mutation records who, when, what, and whether a human, a
// rule, an import or the AI layer caused it.
//
// The origin is written from the first migration rather than added later
// because M11 is what reads it, and an AI-proposed change has to stay
// distinguishable from one a person made for as long as the record exists —
// not only while someone still remembers which was which.

// AuditEntry is one recorded mutation.
type AuditEntry struct {
	ID         string
	OccurredAt time.Time
	ActorID    string
	Origin     Origin
	Action     string
	EntityKind string
	EntityID   string

	// Diff is what changed, as JSON. Postings are immutable, so for the ledger
	// this is what was written rather than a before and an after. Amounts go
	// through Money's own encoding, so the log holds the figure itself and not
	// a rendering of it.
	Diff []byte
}

// writeAudit records one mutation, inside the caller's database transaction.
//
// It takes the transaction rather than the pool on purpose: an audit entry
// that could commit separately from the change it describes would eventually
// describe a change that did not happen.
func writeAudit(
	ctx context.Context, q *Queries, w Write,
	action, entityKind, entityID string, diff []byte,
) error {
	id, err := uuidFrom(w.AuditID)
	if err != nil {
		return err
	}
	actor, err := uuidFrom(w.ActorID)
	if err != nil {
		return err
	}
	entity, err := uuidFrom(entityID)
	if err != nil {
		return err
	}

	if err := q.InsertAuditEntry(ctx, InsertAuditEntryParams{
		ID:         id,
		OccurredAt: timestampFrom(w.at()),
		ActorID:    actor,
		Origin:     string(w.Origin),
		Action:     action,
		EntityKind: entityKind,
		EntityID:   entity,
		Diff:       diff,
	}); err != nil {
		return fmt.Errorf("write audit entry %s: %w", w.AuditID, err)
	}
	return nil
}

// AuditEntriesFor returns the history of one entity, newest first.
func (s *Store) AuditEntriesFor(ctx context.Context, entityKind, entityID string) ([]AuditEntry, error) {
	entity, err := uuidFrom(entityID)
	if err != nil {
		return nil, err
	}

	rows, err := s.ListAuditEntriesForEntity(ctx, ListAuditEntriesForEntityParams{
		EntityKind: entityKind,
		EntityID:   entity,
	})
	if err != nil {
		return nil, fmt.Errorf("list audit entries for %s %s: %w", entityKind, entityID, err)
	}

	entries := make([]AuditEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, AuditEntry{
			ID:         uuidTo(row.ID),
			OccurredAt: timestampTo(row.OccurredAt),
			ActorID:    uuidTo(row.ActorID),
			Origin:     Origin(row.Origin),
			Action:     row.Action,
			EntityKind: row.EntityKind,
			EntityID:   uuidTo(row.EntityID),
			Diff:       row.Diff,
		})
	}
	return entries, nil
}
