import { LanguageSwitcher } from '../components/LanguageSwitcher'
import { Money } from '../components/Money'
import { MoneyInput } from '../components/MoneyInput'
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
