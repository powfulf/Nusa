import type { ReactNode } from 'react'

import { SkeletonRegion } from './Skeleton'

/**
 * A region of the screen that is exactly one of: loading, empty, failed, or
 * ready. Never two.
 *
 * This is where two rules from DESIGN.md are enforced by shape rather than by
 * discipline, per the §11 rule that removing the ability to break a rule beats
 * a check plus a test:
 *
 *   - A region is entirely skeleton or entirely real, never a mixture. There
 *     is no `loading` boolean beside `data`; there is one `state`, and it has
 *     one kind at a time.
 *   - An empty state is never shown while loading, and never for a failure.
 *     `empty` renders only when `state.kind === 'empty'`, and a state that is
 *     both loading and empty is not a value of this type.
 *
 * A boolean triple — `isLoading`, `isError`, `data` — would have allowed
 * `isLoading && data.length === 0` to reach the empty branch, which is the
 * exact lie the rule exists to stop: "you have no transactions" said during a
 * fetch is a false statement about somebody's money. That combination has no
 * spelling here.
 *
 * `region.test.tsx` carries the negative proof: a `@ts-expect-error` on the
 * mixed state, so the guard is the compiler and its failure is a build error.
 */
export type RegionState<T> =
  | { readonly kind: 'loading' }
  | { readonly kind: 'empty' }
  | { readonly kind: 'error'; readonly error: unknown }
  | { readonly kind: 'ready'; readonly data: T }

export interface RegionProps<T> {
  readonly state: RegionState<T>
  /** The skeleton, sized to what `ready` will render. */
  readonly skeleton: () => ReactNode
  /** An <EmptyState>: teaches something, offers one action. */
  readonly empty: () => ReactNode
  /** An error message with an icon and a word — never colour alone. Not an empty state. */
  readonly failed: (error: unknown) => ReactNode
  readonly ready: (data: T) => ReactNode
}

export function Region<T>({ state, skeleton, empty, failed, ready }: RegionProps<T>) {
  switch (state.kind) {
    case 'loading':
      return <SkeletonRegion>{skeleton()}</SkeletonRegion>
    case 'empty':
      return <>{empty()}</>
    case 'error':
      return <div role="alert">{failed(state.error)}</div>
    case 'ready':
      return <>{ready(state.data)}</>
  }
}

/**
 * The one sanctioned bridge from TanStack Query's shape to a RegionState, so
 * the decision "is an empty array the empty state?" is made once, here, and
 * the answer is yes only when the query has settled — never during a fetch.
 */
export function regionStateFrom<T>(query: {
  readonly status: 'pending' | 'error' | 'success'
  readonly data: T | undefined
  readonly error: unknown
}, isEmpty: (data: T) => boolean): RegionState<T> {
  switch (query.status) {
    case 'pending':
      return { kind: 'loading' }
    case 'error':
      return { kind: 'error', error: query.error }
    case 'success':
      if (query.data === undefined) return { kind: 'loading' }
      return isEmpty(query.data) ? { kind: 'empty' } : { kind: 'ready', data: query.data }
  }
}
