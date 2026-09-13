import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import { RouterProvider } from 'react-router'

import { bindPreferencesToRoot } from './preferences/store'
import { router } from './routes'
import './i18n'
import './index.css'

// Before the first render, so the first paint already carries the stored
// preferences: a page that renders with shadows and flattens them a frame
// later has done the one thing a reduced-effects setting exists to prevent.
bindPreferencesToRoot(document.documentElement)

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Financial data must never look fresher than it is. Refetching on focus
      // is the default everywhere in this app.
      refetchOnWindowFocus: true,
      staleTime: 30_000,
    },
  },
})

const container = document.getElementById('root')
if (!container) {
  throw new Error('root element is missing from index.html')
}

createRoot(container).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
)
