import { act, fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { Money as MoneyComponent } from './Money'
import { MoneyInput } from './MoneyInput'
import { Registry } from '../money/commodity'
import { Money } from '../money/money'
import { MoneyProvider } from '../money/context'
import i18n from '../i18n'
import en from '../i18n/en.json'
import id from '../i18n/id.json'

/** U+00A0, written as an escape so the assertions cannot lie by looking right. */
const NBSP = '\u00A0'

const registry = Registry.of([
  { code: 'IDR', kind: 'currency', scale: 2 },
  { code: 'BBCA.JK', kind: 'equity', scale: 0 },
])

function withMoney(node: React.ReactNode, locale = 'id-ID') {
  return render(
    <MoneyProvider registry={registry} locale={locale}>
      {node}
    </MoneyProvider>,
  )
}

beforeEach(async () => {
  await i18n.changeLanguage('en')
})

describe('<Money>', () => {
  it('renders tabular, inline-end aligned digits that never wrap', () => {
    const { container } = withMoney(<MoneyComponent value={Money.of(150_000_000n, 'IDR')} />)
    const el = container.querySelector('span')
    expect(el?.className).toContain('numeric')
    // The separator between symbol and digits is U+00A0, spelled out rather
    // than typed: DESIGN.md requires a non-breaking space so the two never
    // split across lines, and a plain space here would look identical in the
    // source while asserting the opposite of the rule.
    expect(el?.textContent).toBe(`Rp${NBSP}1.500.000,00`)
  })

  it('always shows a minus on a negative, not colour alone', () => {
    const { container } = withMoney(<MoneyComponent value={Money.of(-150_000_000n, 'IDR')} />)
    const el = container.querySelector('span')
    expect(el?.textContent?.includes('-')).toBe(true)
    // The data attribute is what a colour rule would hang off. It accompanies
    // the sign; it is never the only carrier of the fact.
    expect(el?.dataset.negative).toBe('true')
  })

  it('shows a scale-0 commodity with no decimal separator at all', () => {
    const { container } = withMoney(<MoneyComponent value={Money.of(1_250n, 'BBCA.JK')} />)
    expect(container.querySelector('span')?.textContent).toBe(`BBCA.JK${NBSP}1.250`)
  })

  it('drops the symbol when a column header already carries it', () => {
    const { container } = withMoney(
      <MoneyComponent value={Money.of(150_000_000n, 'IDR')} withSymbol={false} />,
    )
    expect(container.querySelector('span')?.textContent).toBe('1.500.000,00')
  })
})

describe('<MoneyInput>', () => {
  it('accepts every shape the field promises, as exact minor units', () => {
    const onChange = vi.fn()
    withMoney(
      <MoneyInput labelKey="health.title" commodity="IDR" value={null} onChange={onChange} />,
    )
    const field = screen.getByLabelText(en.health.title)

    for (const [typed, expected] of [
      ['1500000', 150_000_000n],
      ['1,5 juta', 150_000_000n],
      ['1.500.000', 150_000_000n],
      ['15rb', 1_500_000n],
    ] as const) {
      fireEvent.change(field, { target: { value: typed } })
      const last = onChange.mock.calls.at(-1)?.[0] as Money
      expect(last.amount, typed).toBe(expected)
    }
  })

  it('echoes back what it understood, which is what makes the locale rule safe', () => {
    withMoney(<MoneyInput labelKey="health.title" commodity="IDR" value={null} onChange={vi.fn()} />)
    const field = screen.getByLabelText(en.health.title)

    fireEvent.change(field, { target: { value: '1.5jt' } })

    // Read through aria-describedby rather than by text: Testing Library
    // normalises U+00A0 to a plain space when matching, which would quietly
    // pass an echo that had lost the non-breaking space. Going through the
    // association also proves the echo is what the field points at.
    const describedBy = field.getAttribute('aria-describedby')
    expect(describedBy).toBeTruthy()
    expect(document.getElementById(describedBy ?? '')?.textContent).toBe(
      `Reads as Rp${NBSP}1.500.000,00`,
    )
  })

  it('refuses more precision than the commodity holds, and reports no value', () => {
    const onChange = vi.fn()
    withMoney(
      <MoneyInput labelKey="health.title" commodity="IDR" value={null} onChange={onChange} />,
    )
    const field = screen.getByLabelText(en.health.title)

    fireEvent.change(field, { target: { value: '1,005' } })

    expect(onChange).toHaveBeenLastCalledWith(null)
    expect(field.getAttribute('aria-invalid')).toBe('true')
    // Interpolated from the commodity's own scale, per §7: an explainer uses
    // the reader's own numbers rather than a generic sentence.
    expect(screen.getByRole('alert').textContent).toBe('IDR is counted to 2 decimal places.')
  })

  it('names the error in the reader s language, never in English by default', async () => {
    withMoney(<MoneyInput labelKey="health.title" commodity="IDR" value={null} onChange={vi.fn()} />)
    fireEvent.change(screen.getByLabelText(en.health.title), { target: { value: 'abc' } })
    expect(screen.getByRole('alert').textContent).toBe(en.money.unparseable)

    // Wrapped in act: changing the language re-renders every mounted component,
    // and an unwrapped state update prints a warning that would go on to hide
    // the next real one.
    await act(async () => {
      await i18n.changeLanguage('id')
    })
    expect(screen.getByRole('alert').textContent).toBe(id.money.unparseable)
  })

  it('associates the error with the field for assistive technology', () => {
    withMoney(<MoneyInput labelKey="health.title" commodity="IDR" value={null} onChange={vi.fn()} />)
    const field = screen.getByLabelText(en.health.title)
    fireEvent.change(field, { target: { value: 'abc' } })

    const describedBy = field.getAttribute('aria-describedby')
    expect(describedBy).toBeTruthy()
    expect(document.getElementById(describedBy ?? '')?.textContent).toBe(en.money.unparseable)
  })

  it('carries a permanently visible label, because its fill matches the card', () => {
    withMoney(<MoneyInput labelKey="health.title" commodity="IDR" value={null} onChange={vi.fn()} />)
    // DESIGN.md § Inputs: the label is the control's real marker, and a
    // placeholder is never a substitute because it vanishes when it is needed.
    expect(screen.getByText(en.health.title).tagName).toBe('LABEL')
    expect(screen.getByLabelText(en.health.title).getAttribute('placeholder')).toBeNull()
  })

  it('reports null for an empty field rather than zero', () => {
    const onChange = vi.fn()
    withMoney(
      <MoneyInput labelKey="health.title" commodity="IDR" value={null} onChange={onChange} />,
    )
    const field = screen.getByLabelText(en.health.title)
    fireEvent.change(field, { target: { value: '15' } })
    fireEvent.change(field, { target: { value: '' } })

    // Empty is "nothing entered", zero is "the amount is nought". A field that
    // conflates them writes a zero transaction nobody asked for.
    expect(onChange).toHaveBeenLastCalledWith(null)
    expect(field.getAttribute('aria-invalid')).toBe('false')
  })
})
