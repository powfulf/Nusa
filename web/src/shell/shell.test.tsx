import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, within } from '@testing-library/react'
import i18n from 'i18next'
import { RouterProvider, createMemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import en from '../i18n/en.json'
import { PREFERENCES_STORAGE_KEY, bindPreferencesToRoot, usePreferences } from '../preferences/store'
import { shellRoutes } from '../routes'
import { AppShell } from './AppShell'
import { BOTTOM_BAR_MAX_ITEMS, DESTINATIONS } from './navigation'
import '../i18n'

function renderAt(path: string) {
  const router = createMemoryRouter([{ path: '/', element: <AppShell />, children: shellRoutes }], {
    initialEntries: [path],
  })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

const bottomNav = () => screen.getAllByRole('navigation', { name: en.nav.label })[1] as HTMLElement
const sidebar = () => document.querySelector('aside') as HTMLElement

beforeEach(async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(JSON.stringify({ status: 'ok', service: 'nusa', checks: {} }), { status: 200 })),
  )
  await i18n.changeLanguage('en')
  localStorage.clear()
  usePreferences.setState({ effects: 'normal', density: 'comfortable' })
  delete document.documentElement.dataset.effects
  delete document.documentElement.dataset.density
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('the destination table', () => {
  it('has a route for every destination and a destination for every route', () => {
    const fromTable = DESTINATIONS.map((d) => d.path).sort()
    const fromRoutes = shellRoutes.map((r) => r.path ?? '').sort()
    // Both lists non-empty and equal: a dead link or an unreachable route fails here.
    expect(fromTable.length).toBeGreaterThan(0)
    expect(fromRoutes).toEqual(fromTable)
    for (const r of shellRoutes) {
      expect((r.handle as { titleKey?: string }).titleKey, `${r.path} carries a title`).toBeTypeOf('string')
    }
  })

  it('keeps the bottom bar at the ceiling DESIGN.md sets, with settings sent to the top', () => {
    const bottom = DESTINATIONS.filter((d) => d.bottom)
    expect(bottom.length).toBeGreaterThan(0)
    expect(bottom.length).toBeLessThanOrEqual(BOTTOM_BAR_MAX_ITEMS)
    expect(DESTINATIONS.find((d) => d.path === '/settings')?.bottom).toBe(false)
  })
})

describe('<AppShell>', () => {
  it('swaps navigation at one breakpoint through two complementary class pairs', () => {
    renderAt('/')
    const bar = bottomNav().className.split(' ')
    const side = sidebar().className.split(' ')
    // Below md: bar shown, sidebar hidden. From md: the reverse. No third state.
    expect(bar).toContain('md:hidden')
    expect(bar).not.toContain('hidden')
    expect(side).toContain('hidden')
    expect(side).toContain('md:flex')
    expect(side.some((c) => /^(md:)?transition/.test(c)), 'the swap does not animate').toBe(false)
  })

  it('puts the four destinations in the bottom bar and all five in the sidebar', () => {
    renderAt('/')
    const barLinks = within(bottomNav()).getAllByRole('link')
    const sideLinks = within(sidebar()).getAllByRole('link')
    expect(barLinks.map((a) => a.textContent)).toEqual(
      DESTINATIONS.filter((d) => d.bottom).map((d) => en.nav[keyOf(d.labelKey)]),
    )
    expect(sideLinks).toHaveLength(DESTINATIONS.length)
    expect(sideLinks[sideLinks.length - 1]?.textContent).toBe(en.nav.settings)
  })

  it('gives every bottom-bar item and the top-bar action a 44px hit area, with a gap between items', () => {
    renderAt('/')
    for (const a of within(bottomNav()).getAllByRole('link')) {
      const cls = a.className.split(' ')
      expect(cls, a.textContent ?? '').toContain('min-h-touch')
      expect(cls, a.textContent ?? '').toContain('min-w-touch')
    }
    expect(bottomNav().querySelector('ul')?.className.split(' ')).toContain('gap-xs')
    const gear = within(screen.getByRole('banner')).getByRole('link', { name: en.nav.settings })
    expect(gear.className.split(' ')).toEqual(expect.arrayContaining(['min-h-touch', 'min-w-touch', 'md:hidden']))
  })

  it('carries the wordmark as text in the sidebar and never in the top bar', () => {
    renderAt('/')
    expect(within(sidebar()).getByText(en.app.name)).toBeTruthy()
    expect(within(screen.getByRole('banner')).queryByText(en.app.name)).toBeNull()
  })

  it('titles the top bar from the route, and marks the current destination in both navs', () => {
    renderAt('/accounts')
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe(en.nav.accounts)
    for (const nav of [bottomNav(), sidebar()]) {
      const current = within(nav).getAllByRole('link').filter((a) => a.getAttribute('aria-current') === 'page')
      expect(current).toHaveLength(1)
      expect(current[0]?.textContent).toBe(en.nav.accounts)
      // A bar and a word as well as a colour: the mark is a border utility on the active link only.
      expect(current[0]?.className).toMatch(/border-(t|s)-brand-content/)
    }
  })

  it('renders an unbuilt destination as a not-yet notice with no action', () => {
    renderAt('/transactions')
    const main = screen.getByRole('main')
    expect(within(main).getByRole('heading', { level: 2 }).textContent).toBe(
      en.notYet.heading.replace('{screen}', en.nav.transactions),
    )
    expect(within(main).queryByRole('button'), 'an unbuilt screen offers no action').toBeNull()
  })

  it('adds the safe-area insets where DESIGN.md puts them', () => {
    renderAt('/')
    expect(screen.getByRole('banner').className.split(' ')).toEqual(expect.arrayContaining(['safe-top', 'safe-start', 'safe-end']))
    expect(bottomNav().className.split(' ')).toEqual(expect.arrayContaining(['safe-bottom', 'safe-start', 'safe-end']))
    expect(sidebar().className.split(' ')).toContain('safe-start')
    expect(screen.getByRole('main').className.split(' ')).toContain('clears-bottombar')
  })
})

describe('the settings screen', () => {
  it('flips the root attribute and stores the preference, and clears both on the way back', () => {
    const unbind = bindPreferencesToRoot(document.documentElement)
    try {
      renderAt('/settings')
      const box = screen.getByRole('checkbox', { name: new RegExp(`^${en.settings.effects.label}`) }) as HTMLInputElement
      expect(box.checked).toBe(false)
      expect(document.documentElement.dataset.effects).toBeUndefined()

      act(() => {
        fireEvent.click(box)
      })
      expect(box.checked).toBe(true)
      expect(document.documentElement.dataset.effects).toBe('reduced')
      expect(JSON.parse(localStorage.getItem(PREFERENCES_STORAGE_KEY) ?? '{}').state.effects).toBe('reduced')

      act(() => {
        fireEvent.click(box)
      })
      expect(document.documentElement.dataset.effects, 'removed, not set to a second spelling of normal').toBeUndefined()
      expect(JSON.parse(localStorage.getItem(PREFERENCES_STORAGE_KEY) ?? '{}').state.effects).toBe('normal')
    } finally {
      unbind()
    }
  })

  it('offers density at every width and stores it', () => {
    renderAt('/settings')
    const box = screen.getByRole('checkbox', { name: new RegExp(`^${en.settings.density.label}`) }) as HTMLInputElement
    expect(box.disabled).toBe(false)
    act(() => {
      fireEvent.click(box)
    })
    expect(usePreferences.getState().density).toBe('dense')
    expect(JSON.parse(localStorage.getItem(PREFERENCES_STORAGE_KEY) ?? '{}').state.density).toBe('dense')
  })
})

function keyOf(labelKey: string): keyof typeof en.nav {
  return labelKey.replace('nav.', '') as keyof typeof en.nav
}
