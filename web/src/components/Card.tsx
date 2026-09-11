import type { HTMLAttributes, ReactNode } from 'react'

/**
 * DESIGN.md § Cards.
 *
 * Default has a hairline border and no shadow; Elevated has a shadow and no
 * border. Under `data-effects="reduced"` the elevation tokens flatten to a
 * hairline outline, so an elevated card becomes a default one without the
 * component knowing — which is the point of putting reduced effects in the
 * tokens rather than in every component.
 *
 * The optional header bar is the one tinted surface in the system, and text on
 * it uses `--text-on-inverse`; a status chip placed on it is a chip on a dark
 * surface, which the contrast suite checks as its own pairing.
 */
export interface CardProps extends Omit<HTMLAttributes<HTMLElement>, 'className' | 'title'> {
  readonly variant?: 'default' | 'elevated'
  /** Already-translated text for the tinted header strip. */
  readonly header?: ReactNode
  /** Rendered `as` a section by default; a card that is a list item passes 'li'. */
  readonly as?: 'section' | 'article' | 'li' | 'div'
  readonly children: ReactNode
}

export function Card({ variant = 'default', header, as: Tag = 'section', children, ...rest }: CardProps) {
  return (
    <Tag
      className={[
        'overflow-hidden rounded bg-surface',
        variant === 'elevated' ? 'shadow-md' : 'border border-border-subtle',
      ].join(' ')}
      {...rest}
    >
      {header !== undefined && (
        <div className="on-inverse bg-surface-inverse px-lg py-sm text-caption font-medium uppercase tracking-chip text-text-on-inverse">
          {header}
        </div>
      )}
      <div className="p-lg">{children}</div>
    </Tag>
  )
}
