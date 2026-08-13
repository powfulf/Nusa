import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { App } from './App'
import i18n from './i18n'
import en from './i18n/en.json'
import id from './i18n/id.json'

function renderApp() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })

  return render(
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>,
  )
}

beforeEach(async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            status: 'ok',
            service: 'nusa',
            checks: { database: { status: 'ok', schema_version: 1 } },
          }),
          { status: 200, headers: { 'content-type': 'application/json' } },
        ),
    ),
  )

  await i18n.changeLanguage('en')
})

describe('language switching', () => {
  it('starts in English', () => {
    renderApp()
    expect(screen.getByText(en.health.title)).toBeTruthy()
  })

  it('switches every string to Indonesian when Indonesian is chosen', async () => {
    renderApp()

    fireEvent.click(screen.getByRole('button', { name: id.language.id }))

    await waitFor(() => {
      expect(screen.getByText(id.health.title)).toBeTruthy()
    })
    expect(screen.getByText(id.milestone.notice)).toBeTruthy()
    expect(screen.queryByText(en.health.title)).toBeNull()
  })

  it('switches back to English', async () => {
    renderApp()

    fireEvent.click(screen.getByRole('button', { name: id.language.id }))
    await waitFor(() => expect(screen.getByText(id.health.title)).toBeTruthy())

    fireEvent.click(screen.getByRole('button', { name: en.language.en }))
    await waitFor(() => expect(screen.getByText(en.health.title)).toBeTruthy())
  })
})

describe('health card', () => {
  it('shows the schema version reported by the server', async () => {
    renderApp()

    await waitFor(() => {
      expect(screen.getByText(en.health.ok)).toBeTruthy()
    })
    expect(screen.getByText('1')).toBeTruthy()
  })

  it('reports a transport failure without crashing', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new Error('network down')
      }),
    )

    renderApp()

    await waitFor(() => {
      expect(screen.getByText(en.health.unreachable)).toBeTruthy()
    })
  })
})

describe('catalogs', () => {
  // A key present in one language and missing in another surfaces as a raw
  // key in the interface. Catching that here is cheaper than catching it in
  // production.
  it('define exactly the same keys in both languages', () => {
    const keys = (value: unknown, prefix = ''): string[] => {
      if (typeof value !== 'object' || value === null) return [prefix]
      return Object.entries(value).flatMap(([key, child]) =>
        keys(child, prefix ? `${prefix}.${key}` : key),
      )
    }

    expect(keys(id).sort()).toEqual(keys(en).sort())
  })
})
