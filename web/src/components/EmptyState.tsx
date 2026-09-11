import type { ReactNode } from 'react'

import { Button } from './Button'

/**
 * DESIGN.md § Empty states, which CLAUDE.md §7 makes normative: every empty
 * state teaches something and offers one action.
 *
 * Four parts in a fixed order, and the shape carries the rules rather than
 * documenting them:
 *
 *   - `action` is required and singular. There is no `actions` array and no
 *     secondary button prop, because §7 says one action and an optional second
 *     one becomes a habitual second one.
 *   - `explanation` is a single node. Two sentences of at most twenty words is
 *     a rule about copy and lives in the catalogue review, not in a prop type
 *     that would have to count words at runtime.
 *   - The icon is decorative — the heading says the same thing in words — so
 *     it is `aria-hidden` and may use a text colour rather than a CONTENT one.
 *
 * WHAT THIS COMPONENT CANNOT BE USED FOR is the more important half. It is
 * never shown while data is loading, and never for a failed request: empty is
 * a conclusion, loading is the absence of one, and failed means we do not
 * know. Those are not rules a caller is asked to remember. `<Region>` renders
 * this only when its state is `empty`, and a state that is both loading and
 * empty has no shape in the type.
 */
export interface EmptyStateProps {
  /** A 32px Lucide glyph. Decorative. */
  readonly icon: ReactNode
  /** Already-translated. H3. */
  readonly heading: string
  /** Already-translated. At most two sentences of at most twenty words; may carry one <Explain>. */
  readonly explanation: ReactNode
  /** Already-translated label for the single primary action. */
  readonly action: string
  readonly onAction: () => void
}

export function EmptyState({ icon, heading, explanation, action, onAction }: EmptyStateProps) {
  return (
    <div className="flex flex-col items-center px-md py-2xl text-center">
      <span aria-hidden="true" className="text-text-secondary">
        {icon}
      </span>
      <h3 className="mt-md max-w-narrow text-h3 font-semibold text-text-primary">{heading}</h3>
      <p className="mt-sm max-w-narrow text-body-sm text-text-muted">{explanation}</p>
      <div className="mt-lg">
        <Button variant="primary" size="md" onClick={onAction}>
          {action}
        </Button>
      </div>
    </div>
  )
}
