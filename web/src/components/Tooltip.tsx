import {
  cloneElement,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactElement,
} from 'react'

/**
 * DESIGN.md § Tooltips.
 *
 * Shows after 150ms, hides at once. Opens on hover and on focus, closes on
 * Escape, and is associated with its trigger through aria-describedby, so a
 * screen reader reads it as a description of the control rather than as loose
 * text somewhere on the page.
 *
 * A tooltip never carries information available nowhere else. It is
 * unreachable by touch — there is no hover on a phone — so anything only a
 * tooltip says is something a phone user is never told. The prop name is a
 * reminder of the rule: this is a `hint`, and a hint restates.
 *
 * Rendered inline rather than in a portal. The gallery, the tests and the
 * contrast suite all need the tooltip inside the tree that owns it, and a
 * tooltip that escapes to `<body>` also escapes `data-effects="reduced"` on
 * any ancestor that set it. Overflow clipping is the price; a tooltip lives
 * beside a control, not at the edge of a scroll container.
 */
export interface TooltipProps {
  /** Already-translated hint. Restates something visible; never the only copy of it. */
  readonly hint: string
  /** Exactly one focusable element. The tooltip attaches its description to it. */
  readonly children: ReactElement<{ 'aria-describedby'?: string; onMouseEnter?: () => void }>
  readonly placement?: 'top' | 'bottom'
}

const SHOW_DELAY_MS = 150

export function Tooltip({ hint, children, placement = 'top' }: TooltipProps) {
  const id = useId()
  const [open, setOpen] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const show = () => {
    if (timer.current !== null) return
    timer.current = setTimeout(() => {
      timer.current = null
      setOpen(true)
    }, SHOW_DELAY_MS)
  }
  const hide = () => {
    if (timer.current !== null) {
      clearTimeout(timer.current)
      timer.current = null
    }
    setOpen(false)
  }

  useEffect(() => {
    if (!open) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') hide()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open])

  useEffect(() => () => hide(), [])

  return (
    <span
      className="relative inline-flex"
      onMouseEnter={show}
      onMouseLeave={hide}
      onFocus={show}
      onBlur={hide}
    >
      {cloneElement(children, { 'aria-describedby': id })}
      {/*
        Centred with a full-width flex row rather than a translateX(-50%),
        which is a physical transform and would mis-centre under RTL. inset-x-0
        names both sides with one value and mirrors to itself.
      */}
      <span
        aria-hidden={!open}
        className={[
          'pointer-events-none absolute inset-x-0 z-10 flex justify-center',
          placement === 'top' ? 'bottom-full mb-arrow' : 'top-full mt-arrow',
        ].join(' ')}
      >
        <span
          role="tooltip"
          id={id}
          hidden={!open}
          className={[
            'relative max-w-tooltip rounded bg-surface-inverse px-tooltip-inline py-tooltip-block text-caption text-text-on-inverse',
            'transition-opacity duration-fast ease-standard',
          ].join(' ')}
        >
          {hint}
          {/* The 6px arrow: a rotated square in the tooltip's own fill. */}
          <span
            aria-hidden="true"
            className={[
              'absolute inset-x-0 mx-auto size-arrow rotate-45 bg-surface-inverse',
              placement === 'top' ? '-bottom-arrow' : '-top-arrow',
            ].join(' ')}
          />
        </span>
      </span>
    </span>
  )
}

/** Re-exported so callers can size a delay-dependent test without guessing. */
export const TOOLTIP_SHOW_DELAY_MS: number = SHOW_DELAY_MS
