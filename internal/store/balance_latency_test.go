// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/powfulf/Nusa/internal/ledger"
	"github.com/powfulf/Nusa/internal/store"
)

// Balances are computed by summing postings, with nothing cached (section 5.2
// calls a cache an optimisation, and there was no measurement asking for one).
// This is the measurement.
//
// It is written as a test rather than a Go benchmark because a benchmark
// reports a number and a test can refuse to pass. The point is not to know how
// fast the query is; it is to be told, by a failing build, on the day it stops
// being fast enough. A number nobody is obliged to look at is a number nobody
// looks at.

// The triggers. Crossing either one is the signal to revisit the decision not
// to cache — a checkpoint table or a running balance, both of which this query
// would then be the oracle for.
const (
	// balanceBudget is the p95 a single account's balance must stay under.
	balanceBudget = 50 * time.Millisecond

	// concentrationTrigger is the other half of the rule: however fast the
	// query still is, one account holding this many postings means the shape
	// of the problem has changed and the decision deserves rereading.
	concentrationTrigger = 200_000

	// subtreeBudget is a regression guard, not the trigger above.
	//
	// A subtree balance is a different query with a different shape, and it is
	// measurably worse: at 500,000 postings it runs about nine times slower
	// than the same account's own balance, and the gap widens with size. Why
	// is not yet understood — the plan is a healthy index-only scan at smaller
	// volumes — so this number is deliberately loose. It exists to catch the
	// query getting worse, not to certify that it is fast.
	//
	// Raising it to accommodate a slower measurement would be editing the
	// check. If it fails, find out why first.
	subtreeBudget = 150 * time.Millisecond
)

// realisticAccounts is the order of magnitude a household book reaches. It
// matters: a single account holding half of every posting in the database
// pushes the planner off the index, and that is a fixture artefact rather than
// anything a real book does.
const realisticAccounts = 40

func TestBalanceLatencyStaysWithinItsBudget(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	accounts := seedBenchmarkAccounts(t, s)
	busiest := accounts[0]

	type row struct {
		postings  int
		inAccount int64
		allTime   percentiles
		asOf      percentiles
		subtree   percentiles
	}
	var table []row

	for _, total := range []int{10_000, 100_000, 500_000} {
		resetLedgerFor(t, s)
		loadPostings(t, s, accounts, total)

		var inAccount int64
		require.NoError(t, s.Pool().QueryRow(ctx,
			`SELECT count(*) FROM postings WHERE account_id = $1`, string(busiest)).Scan(&inAccount))

		mid := mustDate(t, "2021-01-01")

		table = append(table, row{
			postings:  total,
			inAccount: inAccount,
			allTime: measure(t, func() {
				_, err := s.Balance(ctx, busiest)
				require.NoError(t, err)
			}),
			asOf: measure(t, func() {
				_, err := s.BalanceAsOf(ctx, busiest, mid)
				require.NoError(t, err)
			}),
			subtree: measure(t, func() {
				_, err := s.SubtreeBalance(ctx, busiest, ledger.Date{})
				require.NoError(t, err)
			}),
		})
	}

	var report strings.Builder
	report.WriteString("\nBalanceAsOf latency, " +
		fmt.Sprintf("%d accounts, busiest account measured\n", realisticAccounts))
	report.WriteString("postings   in account   query          p50        p95        p99\n")
	for _, r := range table {
		for _, m := range []struct {
			name string
			p    percentiles
		}{
			{"Balance", r.allTime},
			{"BalanceAsOf", r.asOf},
			{"SubtreeBalance", r.subtree},
		} {
			fmt.Fprintf(&report, "%-10d %-13d %-14s %-10s %-10s %s\n",
				r.postings, r.inAccount, m.name,
				round(m.p.p50), round(m.p.p95), round(m.p.p99))
		}
	}
	t.Log(report.String())

	for _, r := range table {
		require.Less(t, r.asOf.p95, balanceBudget,
			"BalanceAsOf p95 at %d postings (%d in the account) crossed the budget — "+
				"this is the trigger to revisit caching, not a flaky test",
			r.postings, r.inAccount)
		require.Less(t, r.allTime.p95, balanceBudget,
			"Balance p95 at %d postings crossed the budget", r.postings)
		// Asserted separately and more loosely. Leaving it unasserted was the
		// first version of this test, and it let a 76ms p95 pass unremarked —
		// the check did not cover the query that was slowest.
		require.Less(t, r.subtree.p95, subtreeBudget,
			"SubtreeBalance p95 at %d postings crossed its regression guard", r.postings)
		require.Less(t, int(r.inAccount), concentrationTrigger,
			"one account now holds %d postings, past the %d that says the shape of the "+
				"problem has changed", r.inAccount, concentrationTrigger)
	}
}

type percentiles struct{ p50, p95, p99 time.Duration }

func measure(t *testing.T, run func()) percentiles {
	t.Helper()

	const (
		warmup     = 10
		iterations = 200
	)
	for i := 0; i < warmup; i++ {
		run()
	}

	samples := make([]time.Duration, iterations)
	for i := range samples {
		start := time.Now()
		run()
		samples[i] = time.Since(start)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

	at := func(q float64) time.Duration {
		idx := int(float64(len(samples)-1) * q)
		return samples[idx]
	}
	return percentiles{p50: at(0.50), p95: at(0.95), p99: at(0.99)}
}

func round(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000)
	}
	return d.Round(10 * time.Microsecond).String()
}

func seedBenchmarkAccounts(t *testing.T, s *store.Store) []ledger.AccountID {
	t.Helper()
	ctx := context.Background()

	require.NoError(t, s.SaveUser(ctx, testID("user:bench")))

	ids := make([]ledger.AccountID, 0, realisticAccounts)
	for i := 0; i < realisticAccounts; i++ {
		kind := ledger.AccountExpense
		if i < 10 {
			kind = ledger.AccountAsset
		}
		spec := ledger.AccountSpec{
			ID:        ledger.AccountID(testID(fmt.Sprintf("bench:account:%d", i))),
			Kind:      kind,
			Name:      fmt.Sprintf("Bench account %d", i),
			Commodity: "IDR",
		}
		account, err := ledger.NewAccount(spec)
		require.NoError(t, err)
		require.NoError(t, s.SaveAccount(ctx, account))
		ids = append(ids, spec.ID)
	}
	return ids
}

func resetLedgerFor(t *testing.T, s *store.Store) {
	t.Helper()
	_, err := s.Pool().Exec(context.Background(),
		`TRUNCATE audit_log, idempotency_keys, lot_consumptions, lots, postings, transactions`)
	require.NoError(t, err)
}

// bulkChunk is how many transactions go into one database transaction when
// loading in bulk, and it is not an arbitrary number.
//
// The balance constraint is deferred, so every header and every posting queues
// an after-trigger event that PostgreSQL holds until COMMIT. That queue is not
// free and it does not scale linearly: 300,000 events commit in about eight
// seconds, while 750,000 in a single transaction had not finished after
// thirteen minutes. Chunking keeps each queue small, and costs nothing in
// correctness because a transaction's header and all of its lines stay
// together inside one chunk — which is the only thing the constraint requires.
//
// The importer will meet exactly this. See the M2a notes.
const bulkChunk = 5_000

// loadPostings writes the fixture directly in SQL, in chunks.
//
// Not through SaveTransaction: half a million postings one domain call at a
// time would measure the fixture rather than the query.
func loadPostings(t *testing.T, s *store.Store, accounts []ledger.AccountID, postings int) {
	t.Helper()
	ctx := context.Background()

	transactions := postings / 2
	assets := make([]string, 0, 10)
	expenses := make([]string, 0, len(accounts)-10)
	for i, id := range accounts {
		if i < 10 {
			assets = append(assets, string(id))
		} else {
			expenses = append(expenses, string(id))
		}
	}

	for lo := 1; lo <= transactions; lo += bulkChunk {
		hi := lo + bulkChunk - 1
		if hi > transactions {
			hi = transactions
		}
		loadChunk(t, s, expenses, assets, lo, hi)
	}

	// Without this the visibility map is unset and the covering index cannot
	// answer on its own, so the plan falls back to a bitmap heap scan. That is
	// a property of a table that has just been written, not of the query, and
	// measuring it would measure the fixture again.
	_, err := s.Pool().Exec(ctx, `VACUUM (ANALYZE) postings`)
	require.NoError(t, err)
}

// loadChunk writes one batch of transactions and both of their posting sides
// inside a single database transaction, which is what the deferred constraint
// requires: a header committed apart from its lines is judged alone and
// correctly refused.
func loadChunk(t *testing.T, s *store.Store, expenses, assets []string, lo, hi int) {
	t.Helper()
	ctx := context.Background()

	tx, err := s.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	// Ten years of dates, so an as-of query has a real range to cut through.
	_, err = tx.Exec(ctx, `
		INSERT INTO transactions (id, txn_date)
		SELECT ('01920000-0000-7000-8000-' || lpad(to_hex(n + 1048576), 12, '0'))::uuid,
		       DATE '2016-01-01' + ((n % 3650)::int)
		  FROM generate_series($1::bigint, $2::bigint) AS n`, lo, hi)
	require.NoError(t, err, "insert transactions %d..%d", lo, hi)

	for ordinal, side := range [][]string{expenses, assets} {
		sign := 1
		if ordinal == 1 {
			sign = -1
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO postings (id, transaction_id, txn_date, ordinal, account_id, amount, commodity_code)
			SELECT ('01920000-0000-7000-8000-' || lpad(to_hex(2 * n + 2097152 + $5), 12, '0'))::uuid,
			       ('01920000-0000-7000-8000-' || lpad(to_hex(n + 1048576), 12, '0'))::uuid,
			       DATE '2016-01-01' + ((n % 3650)::int),
			       $5,
			       ($3::uuid[])[(n % array_length($3::uuid[], 1)) + 1],
			       $4 * (((n % 500) + 1) * 1000),
			       'IDR'
			  FROM generate_series($1::bigint, $2::bigint) AS n`,
			lo, hi, side, sign, ordinal)
		require.NoError(t, err, "insert posting side %d for %d..%d", ordinal, lo, hi)
	}

	require.NoError(t, tx.Commit(ctx), "commit chunk %d..%d", lo, hi)
}
