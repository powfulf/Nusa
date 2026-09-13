import { CircleHelp, X } from 'lucide-react'
import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import type { Direction, Magnitude } from '../money/format'

/**
 * CLAUDE.md §7 and DESIGN.md § Explain: a small `?` that opens a three-sentence
 * card using the reader's own numbers.
 *
 * THE NUMBERS ARE REQUIRED BY THE TYPE. `values` is keyed by `term`, so
 * `<Explain term="netWorth" />` with no figures does not compile, and neither
 * does a term handed another term's figures. This is the §11 move of removing
 * the ability to break a rule: a generic card is exactly what §7 calls "just a
 * tooltip", and if a generic card could be written, one would be.
 *
 * Every value is already formatted by the caller — through `formatMoney` and
 * the caller's own locale — and this component never sees a `Money`. It does
 * not know the caller's locale, and §4.5 wants no arithmetic in a display
 * layer. A slot that sits in a directional sentence ("{delta} more", "moved
 * {amount} out of") is typed `Magnitude`, which only `formatMagnitude` can
 * produce, so a signed amount cannot reach a sentence that already says which
 * way it went.
 *
 * It is a popover, not a tooltip. Three sentences are read, hover is not a
 * stable reading surface, and touch has no hover. So: opens on click, Enter or
 * Space; focus moves into the card and RETURNS TO THE TRIGGER on close;
 * Escape, the close control and a click outside all close it; and nothing
 * traps focus — Tab leaves the card in document order. jsdom can see the
 * focus contract; Playwright re-checks it in a real browser.
 *
 * `explain.test.tsx` carries the negative proof for the type, and a guard over
 * the catalogue itself: three sentences, none over twenty words, in every
 * language and every `select` branch.
 */
export interface ExplainValues {
  readonly netWorth: {
    readonly assets: string
    readonly liabilities: string
    readonly netWorth: string
  }
  readonly onPaperGain: {
    readonly costBasis: string
    readonly quantity: string
    readonly marketValue: string
    /** Absolute. The sentence says "more" or "less" from `direction`. */
    readonly delta: Magnitude
    readonly direction: Direction
  }
  readonly fxChange: {
    readonly balance: string
    readonly valueThen: string
    readonly valueNow: string
    readonly reportingCurrency: string
  }
  readonly transaction: {
    /** Absolute. "out of {from} and into {to}" already carries the direction. */
    readonly amount: Magnitude
    readonly from: string
    readonly to: string
    /** The account kind of `to`, as `GET /api/v1/accounts` serves it. */
    readonly toKind: 'asset' | 'liability' | 'equity' | 'income' | 'expense'
  }
}

export type ExplainTerm = keyof ExplainValues

/**
 * The runtime list of terms, for the guards that walk the catalogue. The
 * `satisfies` keeps it from naming a term the map does not have, and the
 * assertion below it keeps it from omitting one — so the list and the type
 * cannot drift apart in either direction.
 */
export const EXPLAIN_TERMS = ['netWorth', 'onPaperGain', 'fxChange', 'transaction'] as const satisfies readonly ExplainTerm[]
type MissingTerm = Exclude<ExplainTerm, (typeof EXPLAIN_TERMS)[number]>
const _everyTermListed: MissingTerm extends never ? true : never = true
void _everyTermListed

/**
 * How far to move the card along the inline axis so it stays inside the
 * viewport with one gutter to spare. Zero when it already fits. A card
 * anchored to a trigger near the inline end would otherwise run off the
 * screen — measured at 360px in the gallery, where it overflowed by 98px.
 *
 * Pure so the arithmetic is unit-testable; only the measurement needs a
 * browser. `rtl` flips which edge counts as the inline end, and therefore the
 * sign of the correction, because inset-inline-start is the right edge there.
 */
export function inlineShiftToFit(
  card: { readonly left: number; readonly right: number },
  viewportWidth: number,
  gutter: number,
  rtl: boolean,
): number {
  if (rtl) {
    const overflow = gutter - card.left
    return overflow > 0 ? overflow : 0
  }
  const overflow = card.right - (viewportWidth - gutter)
  return overflow > 0 ? -overflow : 0
}

export type ExplainProps = {
  [T in ExplainTerm]: {
    readonly term: T
    readonly values: ExplainValues[T]
  }
}[ExplainTerm]

export function Explain({ term, values }: ExplainProps) {
  const { t } = useTranslation()
  const id = useId()
  const headingId = `${id}-heading`
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLSpanElement | null>(null)
  const trigger = useRef<HTMLButtonElement | null>(null)
  const card = useRef<HTMLDivElement | null>(null)

  const title = t(`explain.${term}.title`)

  // Focus follows the card in both directions. Into it on open, so a keyboard
  // reader lands on what they asked for; back to the trigger on close, so they
  // are not dropped at the top of the document. The return is conditional on
  // focus still being inside — a close caused by clicking elsewhere must not
  // yank focus away from where the person just clicked.
  const wasOpen = useRef(false)
  useEffect(() => {
    if (open) {
      wasOpen.current = true
      card.current?.focus()
      return
    }
    // Only on the transition open -> closed: this effect also runs on mount,
    // and a component that steals focus by merely appearing is a bug.
    if (!wasOpen.current) return
    wasOpen.current = false
    const active = document.activeElement
    if (active === null || active === document.body || root.current?.contains(active)) {
      trigger.current?.focus()
    }
  }, [open])

  // Keep the card on screen. Measured, not designed: the offset is whatever
  // the trigger's position demands, so it is set as an inline style rather
  // than drawn from a token. The gutter IS a token, read back from the root so
  // the 16px is not written a second time here.
  useLayoutEffect(() => {
    const el = card.current
    if (!open || el === null) return
    el.style.insetInlineStart = ''
    const rect = el.getBoundingClientRect()
    const rootStyle = getComputedStyle(document.documentElement)
    const gutter = parseFloat(rootStyle.getPropertyValue('--space-md')) || 0
    const rtl = getComputedStyle(el).direction === 'rtl'
    const shift = inlineShiftToFit(rect, document.documentElement.clientWidth, gutter, rtl)
    if (shift !== 0) el.style.insetInlineStart = `${shift}px`
  }, [open])

  useEffect(() => {
    if (!open) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    const onPointerDown = (event: MouseEvent) => {
      if (root.current !== null && !root.current.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('keydown', onKey)
    document.addEventListener('mousedown', onPointerDown)
    return () => {
      document.removeEventListener('keydown', onKey)
      document.removeEventListener('mousedown', onPointerDown)
    }
  }, [open])

  return (
    <span ref={root} className="relative inline-flex align-middle">
      <button
        ref={trigger}
        type="button"
        aria-label={t('explain.trigger', { title })}
        aria-expanded={open}
        aria-controls={id}
        onClick={() => setOpen((was) => !was)}
        className={[
          // The 24px box is the visual; below md the hit area pads to 44px
          // around it without the glyph growing.
          'inline-flex min-h-touch min-w-touch items-center justify-center rounded-sm text-text-secondary md:min-h-explain-trigger md:min-w-explain-trigger',
          'hover:bg-surface-sunken',
          'transition-colors duration-instant ease-standard',
        ].join(' ')}
      >
        <CircleHelp aria-hidden="true" size={16} strokeWidth={1.5} />
      </button>
      {open && (
        <div
          ref={card}
          id={id}
          role="dialog"
          aria-labelledby={headingId}
          tabIndex={-1}
          className="absolute start-0 top-full z-20 mt-xs w-max max-w-explain rounded bg-surface p-md shadow-md"
        >
          <div className="flex items-start justify-between gap-sm">
            <h4 id={headingId} className="text-h4 font-medium text-text-primary">
              {title}
            </h4>
            <button
              type="button"
              aria-label={t('explain.close')}
              onClick={() => setOpen(false)}
              className="inline-flex shrink-0 rounded-sm text-text-secondary hover:bg-surface-sunken"
            >
              <X aria-hidden="true" size={16} strokeWidth={1.5} />
            </button>
          </div>
          <p className="mt-sm text-body-sm text-text-primary">{t(`explain.${term}.body`, values)}</p>
        </div>
      )}
    </span>
  )
}
