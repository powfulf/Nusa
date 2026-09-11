import { CheckCircle2, Landmark, Wallet } from 'lucide-react'

import { FilterChip, StatusChip } from '../components/Chip'
import { Input } from '../components/Input'
import { LanguageSwitcher } from '../components/LanguageSwitcher'
import { List, ListRow } from '../components/List'
import { Money } from '../components/Money'
import { MoneyInput } from '../components/MoneyInput'
import { Table, TableRow, type TableColumn } from '../components/Table'
import { Registry } from '../money/commodity'
import { MoneyProvider } from '../money/context'
import { Money as MoneyValue } from '../money/money'
import type { GalleryRegistry } from './registry'

/**
 * One entry per primitive. `enumeration.test.ts` compares this map against
 * `src/components/*.tsx` in both directions, so a component added without an
 * entry — or an entry left behind after a component is deleted — fails the
 * build.
 *
 * Fixtures only. Nothing here reaches an endpoint: the gallery proves what a
 * primitive looks like in each state, and a state that depends on a server
 * being up is a state the gallery cannot show honestly.
 */

const registry = Registry.of([
  { code: 'IDR', kind: 'currency', scale: 2 },
  { code: 'USD', kind: 'currency', scale: 2 },
  { code: 'EUR', kind: 'currency', scale: 2 },
  { code: 'BTC', kind: 'crypto', scale: 8 },
  { code: 'BBCA.JK', kind: 'equity', scale: 0 },
])

const withMoney = (node: React.ReactNode, locale = 'id-ID') => (
  <MoneyProvider registry={registry} locale={locale}>
    {node}
  </MoneyProvider>
)

const noop = () => undefined

/*
 * Fixture text is deliberately plain Indonesian — payees, descriptions — and
 * not catalogue keys. It stands in for what a person would have typed into
 * their own ledger, which is data, not interface; the gallery's own chrome
 * (headings, state names) goes through i18n like everything else.
 */
const PAYEES = ['Warung Bu Sri', 'Indomaret', 'Gaji', 'Listrik']
const NOTES = ['Makan siang', 'Belanja mingguan', 'Bulan September', 'Tagihan']
const AMOUNTS = [-2_500_000n, -15_000_000n, 850_000_000n, -32_000_000n]

const columns: readonly TableColumn[] = [
  { key: 'date', heading: 'Tanggal' },
  { key: 'payee', heading: 'Penerima' },
  { key: 'amount', heading: 'Jumlah', numeric: true },
]

const idr = (n: bigint) =>
  withMoney(<Money value={MoneyValue.of(n, 'IDR')} withSymbol={false} />)

/** A table with the given rows. Empty is a real state a table has. */
const table = (rows: number) => (
  <Table caption="Transaksi" columns={columns}>
    {Array.from({ length: rows }, (_, i) => (
      <TableRow
        key={i}
        columns={columns}
        selected={i === 1}
        onSelect={noop}
        cells={{
          date: `2026-09-${String(i + 1).padStart(2, '0')}`,
          payee: PAYEES[i % 4],
          amount: idr(AMOUNTS[i % 4] ?? 0n),
        }}
      />
    ))}
  </Table>
)

const list = (rows: number, pending = false) => (
  <List label="Transaksi">
    {Array.from({ length: rows }, (_, i) => (
      <ListRow
        key={i}
        first={i === 0}
        selected={i === 1}
        pending={pending && i === 0}
        onSelect={noop}
        leading={
          i % 2 === 0 ? <Wallet size={20} strokeWidth={2} /> : <Landmark size={20} strokeWidth={2} />
        }
        label={PAYEES[i % 4]}
        description={NOTES[i % 4]}
        trailing={withMoney(<Money value={MoneyValue.of(AMOUNTS[i % 4] ?? 0n, 'IDR')} />)}
      />
    ))}
  </List>
)

export const entries: GalleryRegistry = {
  Money: {
    titleKey: 'gallery.entry.money',
    states: {
      default: {
        render: () =>
          withMoney(
            <div className="flex flex-col gap-xs">
              <Money value={MoneyValue.of(150_000_000n, 'IDR')} />
              <Money value={MoneyValue.of(-125_050n, 'IDR')} />
              <Money value={MoneyValue.of(15_000_000n, 'BTC')} />
              <Money value={MoneyValue.of(1_250n, 'BBCA.JK')} />
            </div>,
          ),
      },
      hover: { notApplicable: 'a rendered amount is not interactive' },
      focus: { notApplicable: 'a rendered amount is not focusable' },
      active: { notApplicable: 'a rendered amount is not interactive' },
      disabled: { notApplicable: 'an amount cannot be disabled; it is a value' },
      loading: { notApplicable: 'a loading amount is a Skeleton, a different primitive' },
      error: { notApplicable: 'an amount that failed to load is an error state of its container' },
      empty: { notApplicable: 'there is no empty amount; absence is rendered by the container' },
    },
  },

  MoneyInput: {
    titleKey: 'gallery.entry.moneyInput',
    states: {
      default: {
        render: () =>
          withMoney(
            <MoneyInput labelKey="gallery.fixture.amount" commodity="IDR" value={null} onChange={noop} />,
          ),
      },
      hover: { interactive: true },
      focus: { interactive: true },
      active: { interactive: true },
      disabled: {
        render: () =>
          withMoney(
            <MoneyInput
              labelKey="gallery.fixture.amount"
              commodity="IDR"
              value={MoneyValue.of(150_000_000n, 'IDR')}
              onChange={noop}
              disabled
            />,
          ),
      },
      loading: { notApplicable: 'an input has no loading state; its form does' },
      error: {
        // The error state is produced by what the person typed, not by a prop,
        // so the fixture types it: a value with more precision than IDR holds.
        render: () => withMoney(<MoneyInputTypedWrong />),
      },
      empty: {
        render: () =>
          withMoney(
            <MoneyInput labelKey="gallery.fixture.amount" commodity="IDR" value={null} onChange={noop} />,
          ),
      },
    },
  },

  FilterChip: {
    titleKey: 'gallery.entry.filterChip',
    states: {
      default: {
        render: () => (
          <div className="flex gap-sm">
            <FilterChip active>Semua</FilterChip>
            <FilterChip>Masuk</FilterChip>
            <FilterChip>Keluar</FilterChip>
          </div>
        ),
      },
      hover: { interactive: true },
      focus: { interactive: true },
      active: { interactive: true },
      disabled: { render: () => <FilterChip disabled>Keluar</FilterChip> },
      loading: { notApplicable: 'a filter is applied instantly; there is nothing to wait for' },
      error: { notApplicable: 'choosing a filter cannot fail' },
      empty: { notApplicable: 'a chip with no label is not permitted; the label identifies it' },
    },
  },

  StatusChip: {
    titleKey: 'gallery.entry.statusChip',
    states: {
      default: {
        render: () => (
          <div className="flex flex-wrap gap-sm">
            <StatusChip tone="success" icon={<CheckCircle2 size={16} strokeWidth={1.5} />}>
              Cocok
            </StatusChip>
            <StatusChip tone="warning">Menunggu</StatusChip>
            <StatusChip tone="error">Lewat batas</StatusChip>
            <StatusChip tone="info">Baru</StatusChip>
          </div>
        ),
      },
      hover: { notApplicable: 'a status chip is a label, not a control' },
      focus: { notApplicable: 'a status chip is a label, not a control' },
      active: { notApplicable: 'a status chip is a label, not a control' },
      disabled: { notApplicable: 'a label cannot be disabled' },
      loading: { notApplicable: 'a label has nothing to load' },
      error: { render: () => <StatusChip tone="error">Lewat batas</StatusChip> },
      empty: { notApplicable: 'a chip with no word carries no meaning and is not permitted' },
    },
  },

  Input: {
    titleKey: 'gallery.entry.input',
    states: {
      default: { render: () => <Input label="Nama akun" helper="Nama yang kamu kenali" /> },
      hover: { interactive: true },
      focus: { interactive: true },
      active: { interactive: true },
      disabled: { render: () => <Input label="Nama akun" defaultValue="BCA Tahapan" disabled /> },
      loading: { notApplicable: 'an input has no loading state; its form does' },
      error: { render: () => <Input label="Nama akun" defaultValue="" error="Akun perlu nama" /> },
      empty: { render: () => <Input label="Nama akun" /> },
    },
  },

  List: {
    titleKey: 'gallery.entry.list',
    states: {
      default: { render: () => list(4) },
      hover: { interactive: true },
      focus: { interactive: true },
      active: { interactive: true },
      disabled: { notApplicable: 'a list is never disabled as a whole; a row may be absent' },
      loading: { notApplicable: 'a loading list is a stack of Skeleton rows, a different primitive' },
      error: { notApplicable: 'a list that failed to load is an error state of its screen' },
      empty: { notApplicable: 'an empty list is an EmptyState, a different primitive' },
    },
  },

  ListRow: {
    titleKey: 'gallery.entry.listRow',
    states: {
      default: { render: () => list(1) },
      hover: { interactive: true },
      focus: { interactive: true },
      active: { interactive: true },
      disabled: { notApplicable: 'a row is never disabled; it is present or it is not' },
      loading: { render: () => list(1, true) },
      error: { notApplicable: 'a row does not fail on its own; its list or its sync does' },
      empty: { notApplicable: 'a row with nothing in it is not rendered' },
    },
  },

  Table: {
    titleKey: 'gallery.entry.table',
    states: {
      default: { render: () => table(4) },
      hover: { interactive: true },
      focus: { interactive: true },
      active: { interactive: true },
      disabled: { notApplicable: 'a table is never disabled as a whole' },
      loading: { notApplicable: 'a loading table is Skeleton rows, a different primitive' },
      error: { notApplicable: 'a table that failed to load is an error state of its screen' },
      empty: { render: () => table(0) },
    },
  },

  TableRow: {
    titleKey: 'gallery.entry.tableRow',
    states: {
      default: { render: () => table(1) },
      hover: { interactive: true },
      focus: { interactive: true },
      active: { interactive: true },
      disabled: { notApplicable: 'a row is never disabled; it is present or it is not' },
      loading: { notApplicable: 'a pending row carries a status chip in a cell; see ListRow' },
      error: { notApplicable: 'a row does not fail on its own' },
      empty: { notApplicable: 'a row with no cells is not rendered' },
    },
  },

  LanguageSwitcher: {
    titleKey: 'gallery.entry.languageSwitcher',
    states: {
      default: { render: () => <LanguageSwitcher /> },
      hover: { interactive: true },
      focus: { interactive: true },
      active: { interactive: true },
      disabled: { notApplicable: 'a language is never unavailable to choose' },
      loading: { notApplicable: 'the catalogue is bundled; there is nothing to wait for' },
      error: { notApplicable: 'choosing a language cannot fail' },
      empty: { notApplicable: 'at least two languages are always offered' },
    },
  },
}

/**
 * A MoneyInput already holding text that IDR cannot represent. The error state
 * of an input is a consequence of input, so the fixture supplies the input.
 */
function MoneyInputTypedWrong() {
  return (
    <MoneyInput
      labelKey="gallery.fixture.amount"
      commodity="IDR"
      value={null}
      onChange={noop}
      // The component reads its initial text from `value`; an unrepresentable
      // value cannot be a Money, so the fixture seeds the text directly.
      initialText="1,005"
    />
  )
}
