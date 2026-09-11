import type { ButtonHTMLAttributes, ReactNode } from 'react'

/**
 * DESIGN.md § Chips.
 *
 * Two families that look alike and are not. A FILTER chip is a control: it is
 * a button, it has a pressed state, and it takes the keyboard. A STATUS chip is
 * a label: it carries a word and a colour and does nothing. Making them one
 * component with a `variant` prop would let a status chip be rendered as a
 * button, which is a control that does nothing — so they are two exports.
 *
 * Uppercase is applied by CSS and never by transforming the string. It is a
 * styling choice some scripts do not have, and the underlying text — what a
 * screen reader gets, what is copied — stays exactly what the catalogue said.
 *
 * Every filter chip carries a permanently visible label, which is what
 * identifies it as a control: its fill matches the page background, so the
 * border is a supporting cue rather than the sole one (§8.2 Controls).
 */

const chipBase =
  'inline-flex items-center gap-xs rounded-sm px-chip-inline py-xs text-caption font-medium uppercase tracking-chip'

export interface FilterChipProps
  extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'type' | 'className'> {
  /** Whether this chip is the selected one. Rendered as aria-pressed, never colour alone. */
  readonly active?: boolean
  readonly children: ReactNode
}

export function FilterChip({ active = false, children, disabled, ...rest }: FilterChipProps) {
  return (
    <button
      type="button"
      aria-pressed={active}
      disabled={disabled}
      className={[
        chipBase,
        // Below md the hit area pads out to 44px without the chip growing
        // visually; at md and above the caption-sized chip is its own size.
        'min-h-touch md:min-h-0',
        'transition-colors duration-fast ease-standard',
        'disabled:cursor-not-allowed disabled:opacity-40',
        active
          ? 'bg-control-primary text-control-primary-text focus-inverse'
          : 'border border-border-strong bg-surface-base text-text-primary hover:bg-surface-sunken',
      ].join(' ')}
      {...rest}
    >
      {children}
    </button>
  )
}

export type StatusTone = 'success' | 'warning' | 'error' | 'info'

/*
 * Whole class names, one per tone, so that Tailwind sees every one of them in
 * the source. Assembling the name from the tone at runtime would leave the
 * utility out of the production bundle — which lint:floor refuses, and which
 * this object is the sanctioned shape for.
 */
const statusTone: Readonly<Record<StatusTone, string>> = {
  success: 'bg-success-surface text-success-content',
  warning: 'bg-warning-surface text-warning-content',
  error: 'bg-error-surface text-error-content',
  info: 'bg-info-surface text-info-content',
}

export interface StatusChipProps {
  readonly tone: StatusTone
  /** The word. Colour never carries the meaning alone (§8.2 Meaning). */
  readonly children: ReactNode
  /** An icon that accompanies the word; decorative, so aria-hidden. */
  readonly icon?: ReactNode
}

export function StatusChip({ tone, children, icon }: StatusChipProps) {
  return (
    <span className={[chipBase, statusTone[tone]].join(' ')}>
      {icon !== undefined && (
        <span aria-hidden="true" className="inline-flex">
          {icon}
        </span>
      )}
      {children}
    </span>
  )
}
