import { beforeEach, describe, expect, it } from 'vitest'

import { PREFERENCES_STORAGE_KEY, applyPreferences, bindPreferencesToRoot, usePreferences } from './store'

beforeEach(() => {
  localStorage.clear()
  usePreferences.setState({ effects: 'normal', density: 'comfortable' })
})

describe('the preferences store', () => {
  it('survives a reload: what was stored is what comes back', async () => {
    usePreferences.getState().setEffects('reduced')
    usePreferences.getState().setDensity('dense')
    const stored = localStorage.getItem(PREFERENCES_STORAGE_KEY)
    expect(stored, 'the write reached storage').not.toBeNull()

    // A reload is a fresh in-memory state hydrated from storage. Reset the
    // memory side to the defaults first, so the assertion cannot pass on what
    // was already there — the store is the subject, not a passenger. The
    // reset itself is persisted (every write is), so the captured bytes go
    // back into storage before hydrating: that is what a reload reads.
    usePreferences.setState({ effects: 'normal', density: 'comfortable' })
    expect(usePreferences.getState().effects).toBe('normal')
    localStorage.setItem(PREFERENCES_STORAGE_KEY, stored as string)
    await usePreferences.persist.rehydrate()
    expect(usePreferences.getState().effects).toBe('reduced')
    expect(usePreferences.getState().density).toBe('dense')
  })

  it('stores only the two preferences, never the setters', () => {
    usePreferences.getState().setEffects('reduced')
    const parsed = JSON.parse(localStorage.getItem(PREFERENCES_STORAGE_KEY) ?? '{}') as { state: Record<string, unknown> }
    expect(Object.keys(parsed.state).sort()).toEqual(['density', 'effects'])
  })
})

describe('applying preferences to the root', () => {
  it('sets the attributes tokens.css selects on, and removes them for the defaults', () => {
    const root = document.createElement('div')
    applyPreferences(root, { effects: 'reduced', density: 'dense' })
    expect(root.dataset.effects).toBe('reduced')
    expect(root.dataset.density).toBe('dense')
    applyPreferences(root, { effects: 'normal', density: 'comfortable' })
    expect(root.hasAttribute('data-effects'), 'absent, so only [data-effects=reduced] ever matches').toBe(false)
    expect(root.hasAttribute('data-density')).toBe(false)
  })

  it('applies the stored state at once and follows changes until unbound', () => {
    usePreferences.getState().setDensity('dense')
    const root = document.createElement('div')
    const unbind = bindPreferencesToRoot(root)
    expect(root.dataset.density, 'applied immediately, before any change').toBe('dense')
    usePreferences.getState().setEffects('reduced')
    expect(root.dataset.effects).toBe('reduced')
    unbind()
    usePreferences.getState().setEffects('normal')
    expect(root.dataset.effects, 'no longer followed after unbinding').toBe('reduced')
  })
})
