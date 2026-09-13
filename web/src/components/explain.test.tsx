import { act, fireEvent, render, screen } from '@testing-library/react'
import i18n from 'i18next'
import { beforeEach, describe, expect, it } from 'vitest'

import en from '../i18n/en.json'
import id from '../i18n/id.json'
import { Registry } from '../money/commodity'
import { directionOf, formatMagnitude, formatMoney, type Magnitude } from '../money/format'
import { Money } from '../money/money'
import { EXPLAIN_TERMS, Explain, inlineShiftToFit, type ExplainValues } from './Explain'
import '../i18n'

/*
 * THE NEGATIVE PROOF, checked by the compiler.
 *
 * Each line is a card §7 forbids, written as a value. If the props ever grow a
 * way to express one, its @ts-expect-error becomes unused and `tsc --noEmit`
 * fails — the guard is the type checker and the break is "make the forbidden
 * thing typecheck".
 */
// @ts-expect-error — a term with no numbers is a tooltip, and a tooltip is not this component
const _generic = <Explain term="netWorth" />
// @ts-expect-error — another term's numbers are not this term's numbers
const _crossed = <Explain term="netWorth" values={{ balance: 'x', valueThen: 'y', valueNow: 'z', reportingCurrency: 'IDR' }} />
// @ts-expect-error — a signed string cannot fill a directional slot; only formatMagnitude makes a Magnitude
const _signed = <Explain term="onPaperGain" values={{ costBasis: 'a', quantity: 'b', marketValue: 'c', delta: '-Rp 1', direction: 'down' }} />
// @ts-expect-error — the direction is not optional: without it the sentence cannot say more or less
const _undirected = <Explain term="onPaperGain" values={{ costBasis: 'a', quantity: 'b', marketValue: 'c', delta: 'x' as Magnitude }} />
void [_generic, _crossed, _signed, _undirected]

const registry = Registry.of([
  { code: 'IDR', kind: 'currency', scale: 2 },
  { code: 'USD', kind: 'currency', scale: 2 },
  { code: 'BBCA.JK', kind: 'equity', scale: 0 },
])

const idr = (units: bigint) => Money.of(units, 'IDR')
const fmt = (m: Money, locale: string) => formatMoney(m, registry, { locale })

beforeEach(async () => {
  await i18n.changeLanguage('en')
})

describe('<Explain>', () => {
  it('renders the reader’s own numbers, formatted by the caller, in both languages', async () => {
    const values = {
      assets: fmt(idr(4_825_000_000n), 'id-ID'),
      liabilities: fmt(idr(1_250_000_000n), 'id-ID'),
      netWorth: fmt(idr(3_575_000_000n), 'id-ID'),
    }
    const { unmount } = render(<Explain term="netWorth" values={values} />)
    fireEvent.click(screen.getByRole('button', { name: 'Explain: What you’re worth' }))
    let body = screen.getByRole('dialog').textContent ?? ''
    expect(body).toContain(values.assets)
    expect(body).toContain(values.liabilities)
    expect(body).toContain(values.netWorth)
    // The apostrophe in "you’re" is typographic, so ICU never reads it as a quote.
    expect(body).toContain('What you’re worth')
    unmount()

    await i18n.changeLanguage('id')
    render(<Explain term="netWorth" values={values} />)
    fireEvent.click(screen.getByRole('button', { name: 'Jelaskan: Kekayaan bersih' }))
    body = screen.getByRole('dialog').textContent ?? ''
    // Compared against the formatter's own output rather than a typed literal:
    // the symbol is joined to the digits by U+00A0, which a typed space is not.
    expect(body).toContain(values.netWorth)
    expect(body).toContain('Kekayaan bersih')
  })

  it('carries a loss as "less" with an unsigned magnitude — never a doubled minus', () => {
    const delta = idr(-125_000_000n)
    const magnitude = formatMagnitude(delta, registry, { locale: 'en-US' })
    expect(magnitude, 'the magnitude of a negative carries no sign').not.toMatch(/[-\u2212]/)
    expect(fmt(delta, 'en-US'), 'while the signed form does — so the two are distinguishable').toMatch(/[-\u2212]/)
    render(
      <Explain
        term="onPaperGain"
        values={{
          costBasis: fmt(idr(2_375_000_000n), 'en-US'),
          quantity: fmt(Money.of(250n, 'BBCA.JK'), 'en-US'),
          marketValue: fmt(idr(2_250_000_000n), 'en-US'),
          delta: magnitude,
          direction: directionOf(delta),
        }}
      />,
    )
    fireEvent.click(screen.getByRole('button'))
    const body = screen.getByRole('dialog').textContent ?? ''
    expect(body).toContain('This loss is on paper')
    expect(body).toContain(`which is ${magnitude} less`)
    expect(body).not.toMatch(/[-\u2212]IDR/)
  })

  it('moves focus into the card on open and back to the trigger on Escape', () => {
    render(<Explain term="fxChange" values={fx} />)
    const trigger = screen.getByRole('button')
    expect(trigger.getAttribute('aria-expanded')).toBe('false')
    act(() => {
      fireEvent.click(trigger)
    })
    const dialog = screen.getByRole('dialog')
    expect(trigger.getAttribute('aria-expanded')).toBe('true')
    expect(trigger.getAttribute('aria-controls')).toBe(dialog.id)
    expect(document.activeElement).toBe(dialog)
    act(() => {
      fireEvent.keyDown(document, { key: 'Escape' })
    })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(document.activeElement, 'focus returns to the trigger').toBe(trigger)
  })

  it('closes from its own close control and returns focus', () => {
    render(<Explain term="fxChange" values={fx} />)
    const trigger = screen.getByRole('button')
    act(() => {
      fireEvent.click(trigger)
    })
    act(() => {
      fireEvent.click(screen.getByRole('button', { name: 'Close explanation' }))
    })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(document.activeElement).toBe(trigger)
  })

  it('closes on a click outside without stealing focus from where the person clicked', () => {
    render(
      <>
        <Explain term="fxChange" values={fx} />
        <input aria-label="elsewhere" />
      </>,
    )
    const trigger = screen.getByRole('button')
    const elsewhere = screen.getByLabelText('elsewhere')
    act(() => {
      fireEvent.click(trigger)
    })
    act(() => {
      elsewhere.focus()
      fireEvent.mouseDown(elsewhere)
    })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(document.activeElement, 'a close by clicking away leaves focus where it landed').toBe(elsewhere)
  })

  it('does not trap focus, and does not take focus by merely mounting', () => {
    render(
      <>
        <Explain term="fxChange" values={fx} />
        <input aria-label="after" />
      </>,
    )
    expect(document.activeElement, 'mounting must not focus the trigger').toBe(document.body)
    act(() => {
      fireEvent.click(screen.getByRole('button'))
    })
    const dialog = screen.getByRole('dialog')
    expect(dialog.hasAttribute('aria-modal')).toBe(false)
    const after = screen.getByLabelText('after')
    act(() => {
      after.focus()
    })
    expect(document.activeElement, 'focus may leave the card while it is open').toBe(after)
    expect(screen.getByRole('dialog'), 'and leaving does not close it — only Escape, close or a click does').toBe(dialog)
  })
})

describe('inlineShiftToFit', () => {
  // The layout half — that the shift is applied and the card lands inside the
  // viewport — is a Playwright measurement. This is the arithmetic.
  it('leaves a card that fits where it is', () => {
    expect(inlineShiftToFit({ left: 130, right: 300 }, 360, 16, false)).toBe(0)
    expect(inlineShiftToFit({ left: 60, right: 230 }, 360, 16, true)).toBe(0)
  })
  it('pulls an overflowing card back to one gutter from the inline end', () => {
    // Measured in the gallery at 360px: left 130, right 458 — 98px over, plus the gutter.
    expect(inlineShiftToFit({ left: 130, right: 458 }, 360, 16, false)).toBe(-114)
    // Under RTL the card grows leftwards from its start edge, so the overflow is on the left.
    expect(inlineShiftToFit({ left: -98, right: 230 }, 360, 16, true)).toBe(114)
  })
})

const fx: ExplainValues['fxChange'] = {
  balance: formatMoney(Money.of(120_000n, 'USD'), registry, { locale: 'en-US' }),
  valueThen: fmt(idr(1_848_000_000n), 'en-US'),
  valueNow: fmt(idr(1_968_000_000n), 'en-US'),
  reportingCurrency: 'IDR',
}

/*
 * THE COPY GUARD. §7: three sentences, none over twenty words, using the
 * reader's numbers. Walked over every language, every term and every branch
 * of every `select`, so a fourth sentence added to one branch of the
 * Indonesian loss case goes red like anything else.
 */
describe('the Explain catalogue', () => {
  type Catalogue = { explain: Record<string, unknown> }
  const catalogues: ReadonlyArray<readonly [string, Catalogue]> = [
    ['en', en],
    ['id', id],
  ]

  // Every select branch a body can take. A term with no select renders once.
  const branches: Readonly<Record<(typeof EXPLAIN_TERMS)[number], ReadonlyArray<Record<string, string>>>> = {
    netWorth: [{}],
    onPaperGain: [{ direction: 'up' }, { direction: 'down' }, { direction: 'flat' }],
    fxChange: [{}],
    transaction: ['asset', 'liability', 'equity', 'income', 'expense'].map((toKind) => ({ toKind })),
  }

  // Placeholder names per term, from the type's own field list — so the check
  // that every number is USED reads the same list the caller has to fill.
  const slots: Readonly<Record<(typeof EXPLAIN_TERMS)[number], readonly string[]>> = {
    netWorth: ['assets', 'liabilities', 'netWorth'],
    onPaperGain: ['costBasis', 'quantity', 'marketValue', 'delta', 'direction'],
    fxChange: ['balance', 'valueThen', 'valueNow', 'reportingCurrency'],
    transaction: ['amount', 'from', 'to', 'toKind'],
  }

  it('names exactly the terms the component knows, in both languages', () => {
    for (const [lng, cat] of catalogues) {
      const keys = Object.keys(cat.explain).filter((k) => k !== 'trigger' && k !== 'close')
      // The JSON is the independent source; EXPLAIN_TERMS is the enumeration.
      expect(keys.length, `${lng}: a catalogue with no terms would make the loop below prove nothing`).toBeGreaterThan(0)
      expect([...keys].sort(), lng).toEqual([...EXPLAIN_TERMS].sort())
    }
  })

  it('keeps every card at three sentences of at most twenty words, in every branch', async () => {
    let rendered = 0
    for (const [lng] of catalogues) {
      await i18n.changeLanguage(lng)
      for (const term of EXPLAIN_TERMS) {
        const shown = new Set<string>()
        for (const branch of branches[term]) {
          // Every slot filled with a one-token value, so a formatted amount
          // counts as one word — "IDR 1,250,000.00" is one number to a reader.
          const values = Object.fromEntries(slots[term].map((s) => [s, `X${s}`]))
          const text = i18n.t(`explain.${term}.body`, { ...values, ...branch })
          rendered += 1
          const sentences = text.split(/(?<=[.!?])\s+/)
          expect(sentences, `${lng} ${term} ${JSON.stringify(branch)}: ${text}`).toHaveLength(3)
          for (const sentence of sentences) {
            const words = sentence.split(/\s+/).filter((w) => w.length > 0)
            expect(words.length, `${lng} ${term} ${JSON.stringify(branch)}: "${sentence}"`).toBeLessThanOrEqual(20)
          }
          for (const s of slots[term]) if (text.includes(`X${s}`)) shown.add(s)
          expect(text, 'a placeholder that survived formatting is an unfilled slot').not.toMatch(/[{}]/)
        }
        // A card that reads correctly with its numbers removed is a tooltip:
        // every number slot is shown in at least one branch. (The "flat"
        // branch of a gain legitimately has no delta to show.)
        for (const s of slots[term]) {
          if (s === 'direction' || s === 'toKind') continue
          expect(shown.has(s), `${lng} ${term}: slot {${s}} is never shown in any branch`).toBe(true)
        }
      }
    }
    // Two languages, four terms, ten branches: not a loop that ran over nothing.
    expect(rendered).toBe(2 * (1 + 3 + 1 + 5))
    const bodies = (cat: Catalogue) =>
      Object.values(cat.explain).filter((v): v is { body: string } => typeof v === 'object' && v !== null && 'body' in v)
    expect(bodies(en)).toHaveLength(EXPLAIN_TERMS.length)
    expect(bodies(id)).toHaveLength(EXPLAIN_TERMS.length)
  })
})
