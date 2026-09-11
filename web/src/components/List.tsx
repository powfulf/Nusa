import { useCallback, useRef, type KeyboardEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusChip } from './Chip'

/**
 * DESIGN.md § Lists and § Dense data patterns.
 *
 * A list is one tab stop. Arrow keys move between rows, Home and End jump to
 * the ends, and Tab leaves the list — which is what lets somebody with a
 * keyboard get past a thousand transactions without pressing Tab a thousand
 * times. The roving tabindex below is the whole mechanism: exactly one row is
 * tabbable at a time, and the arrow keys move which one.
 *
 * Row height, padding and the dense-mode override all come from tokens, so a
 * row never decides its own geometry: `data-density="dense"` on the root
 * changes every row at once and changes nothing else (§ Dense data patterns).
 */
export interface ListProps {
  /** Already-translated accessible name for the list. */
  readonly label: string
  readonly children: ReactNode
}

export function List({ label, children }: ListProps) {
  const ref = useRef<HTMLUListElement>(null)

  const onKeyDown = useCallback((event: KeyboardEvent<HTMLUListElement>) => {
    const rows = [...(ref.current?.querySelectorAll<HTMLElement>('[data-list-row]') ?? [])]
    if (rows.length === 0) return
    const current = rows.findIndex((row) => row === document.activeElement)
    let next: number | null = null
    if (event.key === 'ArrowDown') next = Math.min(rows.length - 1, current + 1)
    else if (event.key === 'ArrowUp') next = Math.max(0, current - 1)
    else if (event.key === 'Home') next = 0
    else if (event.key === 'End') next = rows.length - 1
    if (next === null) return
    event.preventDefault()
    for (const [i, row] of rows.entries()) row.tabIndex = i === next ? 0 : -1
    rows[next]?.focus()
  }, [])

  return (
    <ul
      ref={ref}
      role="listbox"
      aria-label={label}
      onKeyDown={onKeyDown}
      // divide-y for the 1px rule only. The colour is set per row and per
      // side, because a divide-{colour} utility sets border-color on every side
      // of every child at higher specificity than the row's own classes — and
      // painted the selected row's brand-coloured bar in the divider colour.
      // Found by reading the computed style, not by looking.
      className="flex flex-col divide-y"
    >
      {children}
    </ul>
  )
}

export interface ListRowProps {
  /** The primary line. Important labels wrap; they never ellipsise. */
  readonly label: ReactNode
  /** The secondary line, Body SM in --text-muted. */
  readonly description?: ReactNode
  /** A 20px icon at the inline start. Decorative; the label carries meaning. */
  readonly leading?: ReactNode
  /** Content at the inline end — typically a <Money>. */
  readonly trailing?: ReactNode
  readonly selected?: boolean
  /** Waiting to sync: a warning chip AND a word, never colour alone. */
  readonly pending?: boolean
  readonly onSelect?: () => void
  /** The first row in a list is the one that starts tabbable. */
  readonly first?: boolean
}

export function ListRow({
  label,
  description,
  leading,
  trailing,
  selected = false,
  pending = false,
  onSelect,
  first = false,
}: ListRowProps) {
  const { t } = useTranslation()

  return (
    <li
      data-list-row
      role="option"
      aria-selected={selected}
      tabIndex={first ? 0 : -1}
      onClick={onSelect}
      onKeyDown={(event) => {
        if (onSelect !== undefined && (event.key === 'Enter' || event.key === ' ')) {
          event.preventDefault()
          onSelect()
        }
      }}
      className={[
        'flex min-h-row items-center gap-md px-row-inline py-row-block',
        'transition-colors duration-instant ease-standard',
        onSelect !== undefined ? 'cursor-pointer hover:bg-surface-base' : '',
        // The focus ring is inset so the row's own edges do not clip it.
        'focus-inset',
        // Selected: a tinted fill AND a bar at the inline start, per DESIGN.md
        // § Row states — the bar is what survives for a reader who cannot
        // distinguish the tint.
        'border-t-border-divider border-s-2',
        selected ? 'bg-control-selected border-s-brand-content' : 'border-s-transparent',
      ].join(' ')}
    >
      {leading !== undefined && (
        <span aria-hidden="true" className="inline-flex shrink-0 text-text-muted">
          {leading}
        </span>
      )}

      <span className="flex min-w-0 flex-1 flex-col">
        <span className="text-body text-text-primary">{label}</span>
        {description !== undefined && (
          <span className="text-body-sm text-text-muted">{description}</span>
        )}
      </span>

      {pending && <StatusChip tone="warning">{t('list.pending')}</StatusChip>}

      {trailing !== undefined && <span className="shrink-0">{trailing}</span>}
    </li>
  )
}
