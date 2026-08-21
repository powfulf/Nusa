// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/GaffaQ/Nusa/internal/ledger"
)

// The commodities a migration seeds and the commodities ledger.StandardRegistry
// returns are the same fact written twice, in SQL and in Go. Two copies of a
// fact drift, and a scale that drifts moves the decimal point on every amount
// already stored in that commodity.
//
// Nothing prevents the drift. This notices it.
func TestSeededCommoditiesMatchTheStandardRegistry(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	rows, err := s.ListCommodities(ctx)
	require.NoError(t, err)

	registry := ledger.StandardRegistry()
	require.Equal(t, registry.Len(), len(rows),
		"the migration seeds exactly what StandardRegistry knows")

	seen := make(map[ledger.CommodityCode]struct{}, len(rows))
	for _, row := range rows {
		code := ledger.CommodityCode(row.Code)
		seen[code] = struct{}{}

		commodity, ok := registry.Lookup(code)
		require.True(t, ok, "%s is seeded but StandardRegistry does not know it", code)
		require.Equal(t, commodity.Kind().String(), row.Kind, "kind of %s", code)
		require.EqualValues(t, commodity.Scale(), row.Scale, "scale of %s", code)
	}

	for _, code := range registry.Codes() {
		_, ok := seen[code]
		require.True(t, ok, "StandardRegistry knows %s but the migration does not seed it", code)
	}
}

// The seeded set is core only. An exchange's share scale and a fund's unit
// scale are country-specific facts and belong in a Country Pack, so finding
// one here would mean core had quietly grown a country.
func TestSeededCommoditiesCarryNoCountrySpecificEntries(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	rows, err := s.ListCommodities(ctx)
	require.NoError(t, err)

	for _, row := range rows {
		require.Contains(t, []string{"currency", "crypto"}, row.Kind,
			"%s is a %s; equities, funds and metals come from Country Packs", row.Code, row.Kind)
	}
}
