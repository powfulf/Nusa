import { Settings, type LucideIcon } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { NavLink, Outlet, useMatches } from 'react-router'

import { BOTTOM_BAR_MAX_ITEMS, DESTINATIONS } from './navigation'

/**
 * The application shell: top bar, content, and the navigation that is a
 * bottom bar below `md` and a sidebar from `md` up (DESIGN.md § Top bar,
 * § Navigation, § Responsiveness).
 *
 * The swap is two complementary class pairs and nothing else — `md:hidden`
 * on the bar, `hidden md:flex` on the sidebar — so there is no width at which
 * both show or both vanish, and nothing animates between them. Both are in
 * the tree at every width; CSS decides which one paints. That is what makes
 * the swap a plain swap: no state, no measurement, no resize listener that
 * could lag a frame behind the viewport.
 *
 * Safe areas: the top bar adds the top inset, the bottom bar the bottom one,
 * and both add the inline insets for a phone held sideways; the sidebar adds
 * the inline-start inset. Each is painted in the piece's own fill because the
 * inset is padding inside it (index.css).
 *
 * No wordmark in the top bar. It lives in the sidebar, as text through i18n;
 * on a 360px screen the scarcest thing is horizontal space and the most
 * valuable information is *where am I*.
 */
export function AppShell() {
  const { t } = useTranslation()
  const matches = useMatches()
  const titled = matches.map((m) => m.handle).filter(isTitled)
  const titleKey = titled[titled.length - 1]?.titleKey ?? 'app.name'

  // The tab title follows the chosen language rather than staying frozen at
  // whatever the build put there.
  const appName = t('app.name')
  useEffect(() => {
    document.title = appName
  }, [appName])

  const bottomItems = DESTINATIONS.filter((d) => d.bottom)
  if (bottomItems.length > BOTTOM_BAR_MAX_ITEMS) {
    throw new Error(`the bottom bar holds at most ${BOTTOM_BAR_MAX_ITEMS} items (DESIGN.md § Navigation)`)
  }

  return (
    <div className="flex min-h-screen bg-surface-base">
      <aside className="safe-start sticky top-0 hidden h-screen w-sidebar shrink-0 flex-col border-e border-border-subtle bg-surface md:flex">
        <p className="px-md py-lg text-h3 font-semibold text-text-primary">{t('app.name')}</p>
        <nav aria-label={t('nav.label')} className="flex flex-1 flex-col">
          <ul className="flex flex-col">
            {DESTINATIONS.filter((d) => d.bottom).map((d) => (
              <li key={d.path}>
                <SidebarLink path={d.path} label={t(d.labelKey)} Icon={d.icon} />
              </li>
            ))}
          </ul>
          <ul className="mt-auto flex flex-col pb-md">
            {DESTINATIONS.filter((d) => !d.bottom).map((d) => (
              <li key={d.path}>
                <SidebarLink path={d.path} label={t(d.labelKey)} Icon={d.icon} />
              </li>
            ))}
          </ul>
        </nav>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar title={t(titleKey)} settingsLabel={t('nav.settings')} />
        <main className="clears-bottombar safe-start safe-end mx-auto w-full max-w-shell px-md py-xl md:px-xl md:py-2xl">
          <Outlet />
        </main>
      </div>

      <nav
        aria-label={t('nav.label')}
        className="safe-start safe-end safe-bottom fixed inset-x-0 bottom-0 z-10 border-t border-border-subtle bg-surface md:hidden"
      >
        <ul className="flex min-h-bottombar gap-xs">
          {bottomItems.map((d) => (
            <li key={d.path} className="flex min-w-0 flex-1">
              <NavLink
                to={d.path}
                end={d.path === '/'}
                className={({ isActive }) =>
                  [
                    // The whole item is the hit area: full height, full share of the width.
                    'focus-inset flex min-h-touch min-w-touch flex-1 flex-col items-center justify-center gap-xs border-t-2 px-xs text-caption font-medium',
                    'transition-colors duration-instant ease-standard hover:bg-surface-base',
                    isActive ? 'border-t-brand-content text-text-primary' : 'border-t-transparent text-text-muted',
                  ].join(' ')
                }
              >
                <d.icon aria-hidden="true" size={24} strokeWidth={2} />
                <span className="leading-control">{t(d.labelKey)}</span>
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>
    </div>
  )
}

interface SidebarLinkProps {
  readonly path: string
  readonly label: string
  readonly Icon: LucideIcon
}

function SidebarLink({ path, label, Icon }: SidebarLinkProps) {
  return (
    <NavLink
      to={path}
      end={path === '/'}
      className={({ isActive }) =>
        [
          'flex min-h-row items-center gap-sm border-s-2 px-row-inline py-row-block text-body',
          'transition-colors duration-instant ease-standard hover:bg-surface-base',
          isActive ? 'border-s-brand-content bg-control-selected text-text-primary' : 'border-s-transparent text-text-muted',
        ].join(' ')
      }
    >
      <Icon aria-hidden="true" size={24} strokeWidth={2} />
      <span className="leading-control">{label}</span>
    </NavLink>
  )
}

interface TopBarProps {
  readonly title: string
  readonly settingsLabel: string
}

/**
 * DESIGN.md § Top bar. Sticky; gains the `sm` elevation once content scrolls
 * beneath it, reported by a sentinel above the bar in the same way the
 * table's sticky header does it. The height is a minimum: the title wraps
 * and the bar grows.
 *
 * One icon action, and only below `md`: settings, which the sidebar carries
 * from `md` up. Two is the ceiling; this uses one.
 */
function TopBar({ title, settingsLabel }: TopBarProps) {
  const sentinel = useRef<HTMLDivElement>(null)
  const [stuck, setStuck] = useState(false)

  useEffect(() => {
    const el = sentinel.current
    if (el === null || typeof IntersectionObserver === 'undefined') return
    const observer = new IntersectionObserver(([entry]) => {
      setStuck(entry !== undefined && !entry.isIntersecting)
    })
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  return (
    <>
      <div ref={sentinel} aria-hidden="true" />
      <header
        className={[
          'safe-top safe-start safe-end sticky top-0 z-10 border-b border-border-subtle bg-surface',
          'transition-shadow duration-fast ease-standard',
          stuck ? 'shadow-sm' : '',
        ].join(' ')}
      >
        <div className="mx-auto flex min-h-topbar w-full max-w-shell items-center justify-between gap-md px-md md:px-lg">
          <h1 className="text-h3 font-semibold text-text-primary md:text-h2">{title}</h1>
          <NavLink
            to="/settings"
            aria-label={settingsLabel}
            className="inline-flex min-h-touch min-w-touch items-center justify-center rounded text-text-muted hover:bg-surface-sunken md:hidden"
          >
            <Settings aria-hidden="true" size={24} strokeWidth={2} />
          </NavLink>
        </div>
      </header>
    </>
  )
}

interface TitledHandle {
  readonly titleKey: string
}

function isTitled(handle: unknown): handle is TitledHandle {
  return typeof handle === 'object' && handle !== null && typeof (handle as { titleKey?: unknown }).titleKey === 'string'
}
