import { create } from 'zustand'
import { persist } from 'zustand/middleware'

/**
 * UI preferences: the two toggles DESIGN.md gives the settings screen, kept
 * in Zustand (the §3 tool for UI state, adopted at its first use for the same
 * reason TanStack Query was adopted at the first fetch — replacing a hand
 * store in M4 is churn) and persisted so a reload keeps them.
 *
 * Both apply through a data attribute on the root element, which is where the
 * token overrides in tokens.css hang. The store never touches the DOM itself;
 * `bindPreferencesToRoot` does, once, at startup, and the attribute follows
 * every change after that. Keeping the two apart is what lets the store be
 * tested without a document and the DOM binding be tested without a store.
 *
 * Density is stored whatever the screen size. It only takes effect at `md`
 * and above — tokens.css gates the override in a media query — but the
 * preference belongs to the person, not the device: switched on from a phone,
 * it is waiting on the desktop.
 */
export type Effects = 'normal' | 'reduced'
export type Density = 'comfortable' | 'dense'

export interface Preferences {
  readonly effects: Effects
  readonly density: Density
}

interface PreferencesState extends Preferences {
  setEffects(effects: Effects): void
  setDensity(density: Density): void
}

/** The localStorage key. Namespaced like `nusa.language`, which i18n already uses. */
export const PREFERENCES_STORAGE_KEY = 'nusa.preferences'

export const usePreferences = create<PreferencesState>()(
  persist(
    (set) => ({
      effects: 'normal',
      density: 'comfortable',
      setEffects: (effects) => set({ effects }),
      setDensity: (density) => set({ density }),
    }),
    {
      name: PREFERENCES_STORAGE_KEY,
      partialize: (state) => ({ effects: state.effects, density: state.density }),
    },
  ),
)

/**
 * Writes the preferences onto the root as the attributes tokens.css selects
 * on. The attribute is REMOVED for the default rather than set to a default
 * value, so the selector `[data-effects='reduced']` is the only thing that
 * ever matches and there is no second spelling of "normal" to keep in step.
 */
export function applyPreferences(root: HTMLElement, preferences: Preferences): void {
  if (preferences.effects === 'reduced') root.dataset.effects = 'reduced'
  else delete root.dataset.effects
  if (preferences.density === 'dense') root.dataset.density = 'dense'
  else delete root.dataset.density
}

/**
 * Applies the stored preferences now and keeps the root in step afterwards.
 * Called before the first render so the first paint already carries them —
 * a page that renders with shadows and then flattens them a frame later has
 * done the one thing a reduced-effects setting exists to prevent.
 */
export function bindPreferencesToRoot(root: HTMLElement): () => void {
  applyPreferences(root, usePreferences.getState())
  return usePreferences.subscribe((state) => applyPreferences(root, state))
}
