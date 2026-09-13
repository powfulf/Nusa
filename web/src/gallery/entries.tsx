import { AlertTriangle, CheckCircle2, Inbox, Landmark, Plus, Wallet } from 'lucide-react'

import { Button } from '../components/Button'
import { Card } from '../components/Card'
import { Checkbox, Radio } from '../components/Choice'
import { FilterChip, StatusChip } from '../components/Chip'
import { EmptyState } from '../components/EmptyState'
import { Explain } from '../components/Explain'
import { Input } from '../components/Input'
import { Region } from '../components/Region'
import { SkeletonAmount, SkeletonBlock, SkeletonRegion, SkeletonText } from '../components/Skeleton'
import { Tooltip } from '../components/Tooltip'
import { LanguageSwitcher } from '../components/LanguageSwitcher'
import { List, ListRow } from '../components/List'
import { Money } from '../components/Money'
import { MoneyInput } from '../components/MoneyInput'
import { Table, TableRow, type TableColumn } from '../components/Table'
import { Registry } from '../money/commodity'
import { directionOf, formatMagnitude, formatMoney } from '../money/format'
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

/** One region, in whichever of its four states the caller asks for. */
const region = (state: Parameters<typeof Region<string[]>>[0]['state']) => (
  <Region
    state={state}
    skeleton={() => <SkeletonText lines={2} />}
    empty={() => (
      <EmptyState
        icon={<Inbox size={32} strokeWidth={2} />}
        heading="Belum ada transaksi"
        explanation="Transaksi adalah uang yang berpindah. Catat yang pertama."
        action="Catat transaksi"
        onAction={noop}
      />
    )}
    failed={(error) => (
      <span className="flex items-center gap-sm text-body-sm text-error-content">
        <AlertTriangle aria-hidden="true" size={20} strokeWidth={2} />
        {String(error instanceof Error ? error.message : error)}
      </span>
    )}
    ready={(data) => (
      <ul className="flex flex-col gap-xs">
        {data.map((d) => (
          <li key={d} className="text-body text-text-primary">
            {d}
          </li>
        ))}
      </ul>
    )}
  />
)

/** A fixture amount in Rupiah, formatted for id-ID, as a screen would hand it to <Explain>. */
const rupiah = (units: string) => formatMoney(MoneyValue.of(BigInt(units), 'IDR'), registry, { locale: 'id-ID' })

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

  Button: {
    titleKey: 'gallery.entry.button',
    states: {
      default: {
        render: () => (
          <div className="flex flex-wrap items-center gap-sm">
            <Button variant="primary">Simpan</Button>
            <Button variant="secondary">Batal</Button>
            <Button variant="ghost">Lewati</Button>
            <Button variant="destructive">Hapus</Button>
            <Button size="sm" icon={<Plus size={20} />}>Tambah</Button>
            <Button size="lg">Lanjut</Button>
          </div>
        ),
      },
      hover: { interactive: true },
      focus: { interactive: true },
      active: { interactive: true },
      disabled: { render: () => <Button disabled>Simpan</Button> },
      loading: { render: () => <Button loading>Menyimpan</Button> },
      error: { notApplicable: 'a button does not fail; the action it starts does, and that is reported elsewhere' },
      empty: { notApplicable: 'a button without a label is not permitted; the label identifies it' },
    },
  },

  Card: {
    titleKey: 'gallery.entry.card',
    states: {
      default: (
        {
          render: () => (
            <div className="flex flex-col gap-md">
              <Card>Kartu bawaan dengan tepi tipis.</Card>
              <Card variant="elevated">Kartu terangkat dengan bayangan.</Card>
              <Card header="Kategori">Kartu dengan strip kepala.</Card>
            </div>
          ),
        }
      ),
      hover: { notApplicable: 'a card is a surface, not a control; what it holds may be' },
      focus: { notApplicable: 'a card is not focusable; its contents are' },
      active: { notApplicable: 'a card is a surface, not a control' },
      disabled: { notApplicable: 'a surface cannot be disabled' },
      loading: { notApplicable: 'a loading card is a Region in its loading state' },
      error: { notApplicable: 'a failed card is a Region in its error state' },
      empty: { notApplicable: 'an empty card is a Region in its empty state' },
    },
  },

  Checkbox: {
    titleKey: 'gallery.entry.checkbox',
    states: {
      default: {
        render: () => (
          <div className="flex flex-col gap-xs">
            <Checkbox label="Sertakan akun yang ditutup" />
            <Checkbox label="Hanya yang belum dicocokkan" description="Transaksi tanpa pasangan di rekening koran" defaultChecked />
            <Checkbox label="Semua" indeterminate />
          </div>
        ),
      },
      hover: { interactive: true },
      focus: { interactive: true },
      active: { interactive: true },
      disabled: { render: () => <Checkbox label="Sertakan akun yang ditutup" disabled /> },
      loading: { notApplicable: 'a choice has no loading state; its form does' },
      error: { notApplicable: 'a single box cannot be invalid; a group can, and the group reports it' },
      empty: { notApplicable: 'a checkbox with no label is not permitted' },
    },
  },

  Radio: {
    titleKey: 'gallery.entry.radio',
    states: {
      default: {
        render: () => (
          <div className="flex flex-col gap-xs">
            <Radio name="g-kind" value="cash" label="Tunai" defaultChecked />
            <Radio name="g-kind" value="bank" label="Bank" description="Rekening tabungan atau giro" />
            <Radio name="g-kind" value="ewallet" label="Dompet digital" />
          </div>
        ),
      },
      hover: { interactive: true },
      focus: { interactive: true },
      active: { interactive: true },
      disabled: { render: () => <Radio name="g-dis" value="x" label="Tunai" disabled /> },
      loading: { notApplicable: 'a choice has no loading state; its form does' },
      error: { notApplicable: 'a single radio cannot be invalid; the group reports it' },
      empty: { notApplicable: 'a radio with no label is not permitted' },
    },
  },

  Tooltip: {
    titleKey: 'gallery.entry.tooltip',
    states: {
      default: {
        render: () => (
          <div className="flex gap-md pt-2xl">
            <Tooltip hint="Tambah transaksi baru">
              <Button size="sm" icon={<Plus size={20} />}>Tambah</Button>
            </Tooltip>
            <Tooltip hint="Muncul di bawah" placement="bottom">
              <Button size="sm" variant="secondary">Bawah</Button>
            </Tooltip>
          </div>
        ),
      },
      hover: { interactive: true },
      focus: { interactive: true },
      active: { notApplicable: 'a tooltip has no pressed state; it is not a control' },
      disabled: { notApplicable: 'a tooltip on a disabled trigger never opens, because the trigger takes no focus' },
      loading: { notApplicable: 'a tooltip restates something already on screen; there is nothing to fetch' },
      error: { notApplicable: 'a tooltip cannot fail' },
      empty: { notApplicable: 'a tooltip with no hint is not rendered' },
    },
  },

  SkeletonText: {
    titleKey: 'gallery.entry.skeletonText',
    states: {
      default: {
        render: () => (
          <SkeletonRegion>
            <SkeletonText lines={3} />
          </SkeletonRegion>
        ),
      },
      hover: { notApplicable: 'a skeleton is not interactive' },
      focus: { notApplicable: 'a skeleton is not focusable' },
      active: { notApplicable: 'a skeleton is not interactive' },
      disabled: { notApplicable: 'a skeleton has no disabled state' },
      loading: { notApplicable: 'a skeleton IS the loading state' },
      error: { notApplicable: 'a skeleton does not fail; the region that owns it does' },
      empty: { notApplicable: 'a skeleton with zero lines is not rendered' },
    },
  },

  SkeletonAmount: {
    titleKey: 'gallery.entry.skeletonAmount',
    states: {
      default: {
        render: () => (
          <SkeletonRegion>
            <div className="flex flex-col gap-xs">
              <SkeletonAmount />
              <SkeletonAmount />
            </div>
          </SkeletonRegion>
        ),
      },
      hover: { notApplicable: 'a skeleton is not interactive' },
      focus: { notApplicable: 'a skeleton is not focusable' },
      active: { notApplicable: 'a skeleton is not interactive' },
      disabled: { notApplicable: 'a skeleton has no disabled state' },
      loading: { notApplicable: 'a skeleton IS the loading state' },
      error: { notApplicable: 'a skeleton does not fail' },
      empty: { notApplicable: 'a numeric skeleton is always one full-width line' },
    },
  },

  SkeletonBlock: {
    titleKey: 'gallery.entry.skeletonBlock',
    states: {
      default: {
        render: () => (
          <SkeletonRegion>
            <div className="flex items-center gap-md">
              <span className="inline-block size-2xl"><SkeletonBlock height="h-2xl" radius="full" /></span>
              <span className="flex-1"><SkeletonBlock height="h-row" /></span>
            </div>
          </SkeletonRegion>
        ),
      },
      hover: { notApplicable: 'a skeleton is not interactive' },
      focus: { notApplicable: 'a skeleton is not focusable' },
      active: { notApplicable: 'a skeleton is not interactive' },
      disabled: { notApplicable: 'a skeleton has no disabled state' },
      loading: { notApplicable: 'a skeleton IS the loading state' },
      error: { notApplicable: 'a skeleton does not fail' },
      empty: { notApplicable: 'a block with no height is not rendered' },
    },
  },

  SkeletonRegion: {
    titleKey: 'gallery.entry.skeletonRegion',
    states: {
      default: {
        render: () => (
          <SkeletonRegion>
            <SkeletonText lines={2} />
          </SkeletonRegion>
        ),
      },
      hover: { notApplicable: 'a skeleton is not interactive' },
      focus: { notApplicable: 'a skeleton is not focusable' },
      active: { notApplicable: 'a skeleton is not interactive' },
      disabled: { notApplicable: 'a skeleton has no disabled state' },
      loading: { notApplicable: 'a skeleton region IS the loading state' },
      error: { notApplicable: 'a skeleton does not fail' },
      empty: { notApplicable: 'a skeleton region with nothing inside is not rendered' },
    },
  },

  Explain: {
    titleKey: 'gallery.entry.explain',
    states: {
      default: {
        // Four terms, one of each shape: plain figures, a directional select,
        // a rate change, and the transaction card keyed on account kind. The
        // numbers are fixtures formatted by the real formatter; no M3 screen
        // has live figures to hand this component yet (§13).
        render: () => (
          <ul className="flex flex-col gap-sm text-body text-text-primary">
            <li className="flex items-center gap-xs">
              Kekayaan bersih
              <Explain
                term="netWorth"
                values={{
                  assets: rupiah('4825000000'),
                  liabilities: rupiah('1250000000'),
                  netWorth: rupiah('3575000000'),
                }}
              />
            </li>
            <li className="flex items-center gap-xs">
              Untung di atas kertas
              <Explain
                term="onPaperGain"
                values={{
                  costBasis: rupiah('2375000000'),
                  quantity: formatMoney(MoneyValue.of(250n, 'BBCA.JK'), registry, { locale: 'id-ID' }),
                  marketValue: rupiah('2562500000'),
                  delta: formatMagnitude(MoneyValue.of(187_500_000n, 'IDR'), registry, { locale: 'id-ID' }),
                  direction: directionOf(MoneyValue.of(187_500_000n, 'IDR')),
                }}
              />
            </li>
            <li className="flex items-center gap-xs">
              Berubah karena kurs
              <Explain
                term="fxChange"
                values={{
                  balance: formatMoney(MoneyValue.of(120_000n, 'USD'), registry, { locale: 'id-ID' }),
                  valueThen: rupiah('1848000000'),
                  valueNow: rupiah('1968000000'),
                  reportingCurrency: 'IDR',
                }}
              />
            </li>
            <li className="flex items-center gap-xs">
              Transaksi
              <Explain
                term="transaction"
                values={{
                  amount: formatMagnitude(MoneyValue.of(-4_500_000n, 'IDR'), registry, { locale: 'id-ID' }),
                  from: 'Dompet',
                  to: 'Makan',
                  toKind: 'expense',
                }}
              />
            </li>
          </ul>
        ),
      },
      hover: { interactive: true },
      focus: { interactive: true },
      active: { interactive: true },
      disabled: { notApplicable: 'an explanation is never withheld; a term either has one or is not jargon' },
      loading: { notApplicable: 'the numbers arrive with the screen that shows the term; the card never fetches' },
      error: { notApplicable: 'a card that cannot be filled is not rendered — the type refuses a term without its numbers' },
      empty: { notApplicable: 'a term without numbers has no shape; see the @ts-expect-error proof in explain.test.tsx' },
    },
  },

  EmptyState: {
    titleKey: 'gallery.entry.emptyState',
    states: {
      default: {
        render: () => (
          <EmptyState
            icon={<Inbox size={32} strokeWidth={2} />}
            heading="Belum ada transaksi"
            explanation="Transaksi adalah uang yang berpindah. Catat yang pertama."
            action="Catat transaksi"
            onAction={noop}
          />
        ),
      },
      hover: { interactive: true },
      focus: { interactive: true },
      active: { interactive: true },
      disabled: { notApplicable: 'an empty state always offers its one action; a disabled one would teach nothing' },
      loading: { notApplicable: 'never shown while loading — enforced by Region, not by discipline' },
      error: { notApplicable: 'never shown for a failure — enforced by Region, not by discipline' },
      empty: { notApplicable: 'an empty state IS the empty state' },
    },
  },

  Region: {
    titleKey: 'gallery.entry.region',
    states: {
      default: { render: () => region({ kind: 'ready', data: ['Warung Bu Sri', 'Indomaret'] }) },
      hover: { notApplicable: 'a region is a container; its contents may be interactive' },
      focus: { notApplicable: 'a region is not focusable' },
      active: { notApplicable: 'a region is a container' },
      disabled: { notApplicable: 'a region cannot be disabled' },
      loading: { render: () => region({ kind: 'loading' }) },
      error: { render: () => region({ kind: 'error', error: new Error('tidak terhubung') }) },
      empty: { render: () => region({ kind: 'empty' }) },
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
