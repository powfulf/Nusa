import { useTranslation } from 'react-i18next'

/**
 * DESIGN.md § Skeletons.
 *
 * A skeleton is sized to the content it replaces, and the geometry that makes
 * that checkable is here rather than left to each caller:
 *
 *   - A text line's box is the text's own line box, so nothing shifts when the
 *     words arrive. The painted bar is `--skeleton-bar-scale` of the font size
 *     and centred in that box, because a bar filling the whole line lays down
 *     far more ink than the letters will and the page then appears to lighten
 *     as data lands — a change that did not happen.
 *   - The last line of a paragraph is 60%; a numeric column is 100% and
 *     aligned to the inline end, never shortened, because the column's width is
 *     fixed and a short bar misrepresents the alignment.
 *   - The pulse runs on `--motion-pulse`, which both reduced-motion routes
 *     collapse to zero. A skeleton's meaning is its shape.
 *
 * The container is `aria-busy` and the bars are hidden from assistive
 * technology; one visually hidden sentence announces loading once. A screen
 * reader must never read forty empty elements.
 *
 * WHAT IS NOT HERE: a way to show a skeleton beside real content, or an empty
 * state, or an error. A region is entirely skeleton or entirely real, and that
 * is enforced by `<Region>`'s discriminated state rather than by asking
 * callers to remember. This file only knows how to draw.
 */

export interface SkeletonTextProps {
  /** Exactly the number of lines the real content will have. */
  readonly lines: number
  /** Body, Body SM or Caption — the line box follows the type step it replaces. */
  readonly step?: 'body' | 'body-sm' | 'caption'
}

const stepClass: Readonly<Record<NonNullable<SkeletonTextProps['step']>, string>> = {
  body: 'text-body',
  'body-sm': 'text-body-sm',
  caption: 'text-caption',
}

/** Text lines. The last is 60% wide unless there is only one. */
export function SkeletonText({ lines, step = 'body' }: SkeletonTextProps) {
  return (
    <>
      {Array.from({ length: lines }, (_, i) => (
        <span
          key={i}
          aria-hidden="true"
          className={['skeleton-line flex items-center', stepClass[step]].join(' ')}
        >
          <span
            className={[
              'skeleton-bar block rounded-sm bg-skeleton',
              i === lines - 1 && lines > 1 ? 'w-3/5' : 'w-full',
            ].join(' ')}
          />
        </span>
      ))}
    </>
  )
}

/** One line in a numeric column: full width, aligned to the inline end. */
export function SkeletonAmount() {
  return (
    <span aria-hidden="true" className="skeleton-line numeric flex items-center justify-end text-body">
      <span className="skeleton-bar block w-full rounded-sm bg-skeleton" />
    </span>
  )
}

export interface SkeletonBlockProps {
  /** The replaced element's own radius: a card is `default`, an avatar `full`. */
  readonly radius?: 'sm' | 'default' | 'full'
  /** Tailwind height class name, whole — e.g. `h-row`, `h-2xl`. */
  readonly height: string
}

/** A block shape: an image slot, a card, an avatar. */
export function SkeletonBlock({ radius = 'default', height }: SkeletonBlockProps) {
  const radiusClass = radius === 'sm' ? 'rounded-sm' : radius === 'full' ? 'rounded-full' : 'rounded'
  return <span aria-hidden="true" className={['block w-full bg-skeleton', radiusClass, height].join(' ')} />
}

/**
 * The container every skeleton sits in. Announces loading once, marks itself
 * busy, and carries the pulse for everything inside it — one animation per
 * region rather than one per bar.
 */
export function SkeletonRegion({ children }: { readonly children: React.ReactNode }) {
  const { t } = useTranslation()
  return (
    <div aria-busy="true" className="animate-pulse">
      <span className="sr-only" role="status">
        {t('skeleton.loading')}
      </span>
      {children}
    </div>
  )
}
