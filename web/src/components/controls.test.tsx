import { act, fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { Button } from './Button'
import { Card } from './Card'
import { Checkbox, Radio } from './Choice'
import { TOOLTIP_SHOW_DELAY_MS, Tooltip } from './Tooltip'

describe('<Button>', () => {
  it('defaults to type=button, so a stray button inside a form does not submit it', () => {
    render(<Button>Save</Button>)
    expect(screen.getByRole('button').getAttribute('type')).toBe('button')
  })

  it('is disabled and busy while loading, and keeps its label', () => {
    render(<Button loading>Save</Button>)
    const b = screen.getByRole('button')
    expect(b.hasAttribute('disabled')).toBe(true)
    expect(b.getAttribute('aria-busy')).toBe('true')
    expect(b.textContent).toBe('Save')
  })

  it('uses the inverse focus ring on the two dark fills and not on the light ones', () => {
    for (const [variant, inverse] of [
      ['primary', true],
      ['destructive', true],
      ['secondary', false],
      ['ghost', false],
    ] as const) {
      const r = render(<Button variant={variant}>x</Button>)
      const has = r.getByRole('button').className.split(' ').includes('focus-inverse')
      expect(has, variant).toBe(inverse)
      r.unmount()
    }
  })

  it('has a minimum height, never a fixed one, and never ellipsises its label', () => {
    // DESIGN.md § Buttons: a wrapped label grows the button. jsdom does not
    // lay out, so the layout half (47/55/64px at two lines) is a Playwright
    // measurement; this half catches the class that would break it — a fixed
    // h-*, truncate, or nowrap — before any browser sees it.
    for (const size of ['sm', 'md', 'lg'] as const) {
      const r = render(<Button size={size}>x</Button>)
      const cls = r.getByRole('button').className.split(' ')
      expect(cls.some((c) => /^(md:)?min-h-/.test(c)), size).toBe(true)
      expect(cls.some((c) => /^(md:)?h-/.test(c)), size).toBe(false)
      expect(cls).not.toContain('truncate')
      expect(cls).not.toContain('whitespace-nowrap')
      expect(cls).toContain('leading-control')
      r.unmount()
    }
  })

  it('hides a decorative icon from assistive technology', () => {
    const { container } = render(<Button icon={<svg data-testid="i" />}>Go</Button>)
    expect(container.querySelector('[aria-hidden="true"] [data-testid="i"]')).toBeTruthy()
  })
})

describe('<Card>', () => {
  it('is bordered by default and shadowed when elevated, never both', () => {
    const a = render(<Card>x</Card>)
    const cls = a.container.firstElementChild?.className.split(' ') ?? []
    expect(cls).toContain('border')
    expect(cls).not.toContain('shadow-md')
    a.unmount()
    const b = render(<Card variant="elevated">x</Card>)
    const cls2 = b.container.firstElementChild?.className.split(' ') ?? []
    expect(cls2).toContain('shadow-md')
    expect(cls2).not.toContain('border')
  })

  it('renders the header on the inverse surface with inverse text and inverse focus', () => {
    render(<Card header="Category">x</Card>)
    const header = screen.getByText('Category')
    const cls = header.className.split(' ')
    expect(cls).toContain('bg-surface-inverse')
    expect(cls).toContain('text-text-on-inverse')
    expect(cls).toContain('on-inverse')
  })
})

describe('<Checkbox> and <Radio>', () => {
  it('always has a visible label associated with the input', () => {
    render(<Checkbox label="Include closed accounts" />)
    const box = screen.getByLabelText('Include closed accounts')
    expect(box.getAttribute('type')).toBe('checkbox')
    expect(screen.getByText('Include closed accounts').closest('label')).toBeTruthy()
  })

  it('exposes indeterminate as the DOM property, which no attribute can carry', () => {
    render(<Checkbox label="All" indeterminate />)
    expect((screen.getByLabelText('All') as HTMLInputElement).indeterminate).toBe(true)
  })

  it('keeps a radio group exclusive through the native input', () => {
    render(
      <>
        <Radio name="kind" value="cash" label="Cash" defaultChecked />
        <Radio name="kind" value="bank" label="Bank" />
      </>,
    )
    const cash = screen.getByLabelText('Cash') as HTMLInputElement
    const bank = screen.getByLabelText('Bank') as HTMLInputElement
    expect(cash.checked).toBe(true)
    fireEvent.click(bank)
    expect(bank.checked).toBe(true)
    expect(cash.checked).toBe(false)
  })

  it('draws the checked state with a glyph as well as a fill', () => {
    // The glyph is what survives for a reader who cannot tell the two fills
    // apart. It is present in the tree regardless of state and shown by CSS.
    const { container } = render(<Checkbox label="A" defaultChecked />)
    expect(container.querySelectorAll('svg[aria-hidden="true"]').length).toBeGreaterThanOrEqual(1)
  })
})

describe('<Tooltip>', () => {
  it('describes its trigger, shows after the delay, and hides at once', () => {
    vi.useFakeTimers()
    try {
      render(
        <Tooltip hint="Adds a transaction">
          <button type="button">+</button>
        </Tooltip>,
      )
      const trigger = screen.getByRole('button')
      const tip = document.getElementById(trigger.getAttribute('aria-describedby') ?? '')
      expect(tip?.getAttribute('role')).toBe('tooltip')
      expect(tip?.hidden).toBe(true)

      act(() => {
        fireEvent.focus(trigger)
      })
      expect(tip?.hidden, 'not yet — the delay has not elapsed').toBe(true)
      act(() => {
        vi.advanceTimersByTime(TOOLTIP_SHOW_DELAY_MS)
      })
      expect(tip?.hidden).toBe(false)

      act(() => {
        fireEvent.blur(trigger)
      })
      expect(tip?.hidden, 'hidden immediately, no delay on the way out').toBe(true)
    } finally {
      vi.useRealTimers()
    }
  })

  it('closes on Escape', () => {
    vi.useFakeTimers()
    try {
      render(
        <Tooltip hint="Hint">
          <button type="button">+</button>
        </Tooltip>,
      )
      const trigger = screen.getByRole('button')
      act(() => {
        fireEvent.focus(trigger)
        vi.advanceTimersByTime(TOOLTIP_SHOW_DELAY_MS)
      })
      expect(screen.getByRole('tooltip').hidden).toBe(false)
      act(() => {
        fireEvent.keyDown(document, { key: 'Escape' })
      })
      expect(document.querySelector('[role="tooltip"]')?.hasAttribute('hidden')).toBe(true)
    } finally {
      vi.useRealTimers()
    }
  })
})
