import { createBrowserRouter, type RouteObject } from 'react-router'

import { App } from './App'

/**
 * The route table.
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
const routes: RouteObject[] = [
  { path: '/', element: <App /> },
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
