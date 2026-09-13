import { createBrowserRouter, type RouteObject } from 'react-router'

import { NotYet } from './screens/NotYet'
import { Overview } from './screens/Overview'
import { Settings } from './screens/Settings'
import { AppShell } from './shell/AppShell'
import { DESTINATIONS } from './shell/navigation'

/**
 * The route table.
 *
 * Every destination in `DESTINATIONS` has a route here, and shell.test.tsx
 * checks the two lists agree in both directions — a destination with no
 * route is a dead link, and a route with no destination is unreachable. Each
 * route carries its title key in `handle`, which is where the top bar reads
 * the page title from.
 *
 * Destinations the product has not built render <NotYet>, never an empty
 * state (DESIGN.md § Empty states): the shell has fetched nothing, so it
 * cannot claim anybody's data is empty, and a button that leads nowhere is a
 * lie about the big button.
 *
 * The gallery is registered only in development mode, and loaded lazily. Both
 * halves matter: `import.meta.env.MODE` is replaced with a string literal at
 * build time, so under `--mode production` the whole branch — including the
 * dynamic import — is dead code to Rollup and the gallery module never enters
 * the bundle. That claim is tested by `scripts/check-gallery-bundle.mjs`
 * rather than trusted, because "non-production" is a statement about our own
 * code and §11 makes such a statement a candidate for testing, not a premise.
 *
 * It is MODE and not DEV on purpose. `import.meta.env.DEV` is false under
 * `vite build` in every mode, so a development-mode build could never contain
 * the gallery, and the bundle check's second assertion — that a development
 * build DOES contain it — would have nothing to compare against. The first
 * version of this file used DEV; the check's own "reads nothing" guard is what
 * caught it, on its first run.
 */
const built: Readonly<Record<string, () => React.JSX.Element>> = {
  '/': () => <Overview />,
  '/settings': () => <Settings />,
}

export const shellRoutes: RouteObject[] = DESTINATIONS.map((d) => {
  const render = built[d.path]
  return {
    path: d.path,
    element: render !== undefined ? render() : <NotYet screenKey={d.labelKey} />,
    handle: { titleKey: d.labelKey },
  }
})

const routes: RouteObject[] = [
  { path: '/', element: <AppShell />, children: shellRoutes },
  ...(import.meta.env.MODE === 'development'
    ? [
        {
          path: '/gallery',
          lazy: async () => {
            const { default: Component } = await import('./gallery/GalleryRoute')
            return { Component }
          },
        },
      ]
    : []),
]

export const router = createBrowserRouter(routes)
