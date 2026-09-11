import { useEffect, useRef, useState, type ReactNode } from 'react'

/**
 * DESIGN.md § Dense data patterns.
 *
 * A transaction register is dense, and that is the shape of the product.
 * Density is not the problem; unstructured density is. What makes a long
 * numeric list calm is all here and none of it is optional:
 *
 *   - Separators, never zebra striping. Stripes shuffle under sorting,
 *     filtering and virtual scrolling; a rule between rows survives all three.
 *   - A sticky header that gains the `sm` elevation once content scrolls
 *     beneath it. The elevation is the cue that there is something under the
 *     header, so it appears only then — a sentinel above the table reports
 *     when it has scrolled out of view.
 *   - Numeric columns aligned to the inline end with tabular figures, never
 *     truncated. The column widens or the label wraps; the digits do not.
 *   - Below `md` the table reflows into cards. Semantics stay a <table> —
 *     a screen reader still gets rows and headers — and the CSS in index.css
 *     stacks each cell with its column heading in front of it, taken from
 *     data-label. Amounts keep their inline-end alignment inside the card,
 *     because losing that is the single biggest loss when a table reflows.
 *
 * Row geometry comes from the same tokens as <ListRow>, so dense mode reaches
 * both at once.
 */
export interface TableColumn {
  readonly key: string
  /** Already-translated heading. Also the label each cell carries when reflowed. */
  readonly heading: string
  readonly numeric?: boolean
}

export interface TableProps {
  /** Already-translated caption. Read by assistive technology; visually hidden. */
  readonly caption: string
  readonly columns: readonly TableColumn[]
  readonly children: ReactNode
}

export function Table({ caption, columns, children }: TableProps) {
  const sentinel = useRef<HTMLDivElement>(null)
  const [scrolled, setScrolled] = useState(false)

  useEffect(() => {
    const el = sentinel.current
    if (el === null || typeof IntersectionObserver === 'undefined') return
    const observer = new IntersectionObserver(([entry]) => {
      setScrolled(entry !== undefined && !entry.isIntersecting)
    })
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  return (
    <div className="relative">
      {/* Zero-height marker. Once it leaves the viewport, the header is over content. */}
      <div ref={sentinel} aria-hidden="true" />
      <table className="table-reflow w-full border-collapse">
        <caption className="sr-only">{caption}</caption>
        <thead
          data-scrolled={scrolled ? 'true' : undefined}
          className={[
            'sticky top-0 bg-surface transition-shadow duration-fast ease-standard',
            scrolled ? 'shadow-sm' : 'shadow-none',
          ].join(' ')}
        >
          <tr>
            {columns.map((column) => (
              <th
                key={column.key}
                scope="col"
                className={[
                  'border-b border-border-subtle px-row-inline py-row-block text-caption font-medium uppercase tracking-chip text-text-secondary',
                  column.numeric ? 'text-end' : 'text-start',
                ].join(' ')}
              >
                {column.heading}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-border-divider">{children}</tbody>
      </table>
    </div>
  )
}

export interface TableRowProps {
  readonly columns: readonly TableColumn[]
  /** One cell per column, keyed by column key. */
  readonly cells: Readonly<Record<string, ReactNode>>
  readonly selected?: boolean
  readonly onSelect?: () => void
}

export function TableRow({ columns, cells, selected = false, onSelect }: TableRowProps) {
  return (
    <tr
      aria-selected={onSelect !== undefined ? selected : undefined}
      tabIndex={onSelect !== undefined ? 0 : undefined}
      onClick={onSelect}
      onKeyDown={(event) => {
        if (onSelect !== undefined && (event.key === 'Enter' || event.key === ' ')) {
          event.preventDefault()
          onSelect()
        }
      }}
      className={[
        'focus-inset transition-colors duration-instant ease-standard',
        onSelect !== undefined ? 'cursor-pointer hover:bg-surface-base' : '',
        selected ? 'bg-control-selected' : '',
      ].join(' ')}
    >
      {columns.map((column) => (
        <td
          key={column.key}
          data-label={column.heading}
          className={[
            'min-h-row px-row-inline py-row-block text-body text-text-primary',
            column.numeric ? 'numeric' : 'text-start',
          ].join(' ')}
        >
          {cells[column.key]}
        </td>
      ))}
    </tr>
  )
}
