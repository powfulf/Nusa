import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import GalleryRoute from './GalleryRoute'
import { entries } from './entries'
import { REQUIRED_STATES } from './registry'
import '../i18n'

/*
 * Every fixture renders, in both effects modes, without throwing.
 *
 * The enumeration guard proves an entry EXISTS for every state; this proves
 * the entry's render function actually produces something. A fixture that
 * throws on render is an entry in name only, and nothing about the registry's
 * shape would say so.
 */
describe('the gallery route', () => {
  it('renders every declared fixture twice — as shipped and with reduced effects', () => {
    const { container } = render(
      <QueryClientProvider client={new QueryClient()}>
        <GalleryRoute />
      </QueryClientProvider>,
    )

    let expected = 0
    for (const entry of Object.values(entries)) {
      for (const state of REQUIRED_STATES) {
        if ('render' in entry.states[state]) expected += 1
      }
    }
    // Both sides asserted non-empty before they are compared.
    expect(expected).toBeGreaterThan(0)

    const rendered = container.querySelectorAll('[data-gallery-render]').length
    expect(rendered).toBe(expected * 2)

    const reduced = container.querySelectorAll('[data-effects="reduced"] [data-gallery-render]').length
    expect(reduced).toBe(expected)
  })

  it('marks each state cell with the kind of claim it makes', () => {
    const { container } = render(
      <QueryClientProvider client={new QueryClient()}>
        <GalleryRoute />
      </QueryClientProvider>,
    )
    const kinds = new Set(
      [...container.querySelectorAll('[data-gallery-kind]')].map((el) =>
        el.getAttribute('data-gallery-kind'),
      ),
    )
    // Playwright reads these to decide what to drive; all three kinds must be
    // present in the corpus or the interactive path is never exercised.
    expect(kinds).toEqual(new Set(['render', 'interactive', 'n/a']))
  })
})
