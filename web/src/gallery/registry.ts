import type { ReactNode } from 'react'

/**
 * The gallery registry: every primitive in `src/components/`, in every state.
 *
 * This is the replacement for Storybook, and the one thing Storybook never
 * had. A component with no story is invisible to Storybook; a component with
 * no entry here fails the build (`enumeration.test.ts`). "A component without
 * a complete entry is unfinished" was never enforceable until it was
 * enumerated.
 *
 * The gallery is a route inside the application, so every entry renders under
 * the same tokens, stylesheet and providers a user gets. There is no second
 * world to keep in step — which is the §11 reason Storybook was declined: an
 * isolated environment is a fake, and a fake is proved against a world that
 * does not ship.
 *
 * The gallery is NOT in the production bundle, and that is a claim about our
 * own code, so it is tested (`scripts/check-gallery-bundle.mjs`) rather than
 * assumed. `GALLERY_MARKER` is what that check looks for.
 */

/** Unique enough that only this module can put it in a bundle. */
export const GALLERY_MARKER = 'nusa-gallery-marker-7f3a2c'

/**
 * The states every primitive must account for. DESIGN.md § Component states
 * lists default, hover, focus-visible, active, disabled, loading and error;
 * the M3 prompt adds empty. Reduced effects is not a per-entry state: the
 * gallery renders every entry a second time under `data-effects="reduced"`.
 */
export const REQUIRED_STATES = [
  'default',
  'hover',
  'focus',
  'active',
  'disabled',
  'loading',
  'error',
  'empty',
] as const

export type StateKey = (typeof REQUIRED_STATES)[number]

/**
 * How one state is accounted for. Exactly one of three, and each is a
 * different kind of claim:
 *
 *   - `render`: the state is a prop or data shape, and this produces it.
 *   - `interactive`: the state is a pointer or keyboard condition (hover,
 *     focus, active) that a static page cannot force honestly. Playwright
 *     drives it against the default render. Declaring it here is what tells
 *     Playwright there is something to drive.
 *   - `notApplicable`: the component genuinely has no such state, and the
 *     reason says why. The reason is for the reader and the guard, not the
 *     page — it is developer text and is never rendered.
 */
export type StateSpec =
  | { readonly render: () => ReactNode }
  | { readonly interactive: true }
  | { readonly notApplicable: string }

export interface GalleryEntry {
  /** Message-catalogue key for the entry's heading. */
  readonly titleKey: string
  readonly states: Readonly<Record<StateKey, StateSpec>>
}

export type GalleryRegistry = Readonly<Record<string, GalleryEntry>>
