import { ArrowLeftRight, Landmark, LayoutDashboard, Mail, Settings, type LucideIcon } from 'lucide-react'

/**
 * The destinations, in the order the sidebar lists them. One table, read by
 * the bottom bar, the sidebar, the top bar's title and the route table, so
 * the four cannot disagree about what exists.
 *
 * `bottom` marks the four that go in the bottom bar (DESIGN.md § Navigation:
 * at most four, because a fifth leaves "Transactions" no room at 360px).
 * Settings is not one of them — it is the rarely used destination that
 * § Responsiveness sends to the top of the screen, where below `md` it is an
 * icon action on the top bar.
 */
export interface Destination {
  readonly path: string
  /** Catalogue key for both the nav label and the page title. */
  readonly labelKey: string
  readonly icon: LucideIcon
  readonly bottom: boolean
}

export const DESTINATIONS: readonly Destination[] = [
  { path: '/', labelKey: 'nav.overview', icon: LayoutDashboard, bottom: true },
  { path: '/transactions', labelKey: 'nav.transactions', icon: ArrowLeftRight, bottom: true },
  { path: '/accounts', labelKey: 'nav.accounts', icon: Landmark, bottom: true },
  { path: '/envelopes', labelKey: 'nav.envelopes', icon: Mail, bottom: true },
  { path: '/settings', labelKey: 'nav.settings', icon: Settings, bottom: false },
]

/** The ceiling DESIGN.md § Navigation sets, asserted by shell.test.tsx against the table. */
export const BOTTOM_BAR_MAX_ITEMS = 4
