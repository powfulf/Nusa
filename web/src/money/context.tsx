import { createContext, useContext, useMemo, type ReactNode } from 'react'

import { Registry } from './commodity'

/**
 * Where a component finds a scale and a locale.
 *
 * Every rendered amount needs both, and a transaction table renders hundreds —
 * threading two props through every row is what context exists to avoid.
 *
 * Language and locale are separate concerns (§9). The language decides which
 * catalogue a string comes from; the locale decides how a number, a date and a
 * currency are written. Somebody reading the interface in English while living
 * with Rupiah is an ordinary case, not an edge one, so this reads the browser's
 * locale rather than following `i18n.resolvedLanguage`.
 */
interface MoneyContextValue {
  readonly registry: Registry
  readonly locale: string
}

function detectLocale(): string {
  const candidate = globalThis.navigator?.language
  return typeof candidate === 'string' && candidate.length > 0 ? candidate : 'en-US'
}

const MoneyContext = createContext<MoneyContextValue>({
  registry: Registry.empty(),
  locale: 'en-US',
})

export interface MoneyProviderProps {
  /** Commodities from `GET /api/v1/commodities`. Empty until they arrive. */
  readonly registry?: Registry
  /** Overrides the detected locale. The settings screen supplies this later. */
  readonly locale?: string
  readonly children: ReactNode
}

export function MoneyProvider({ registry, locale, children }: MoneyProviderProps) {
  const value = useMemo(
    () => ({ registry: registry ?? Registry.empty(), locale: locale ?? detectLocale() }),
    [registry, locale],
  )
  return <MoneyContext.Provider value={value}>{children}</MoneyContext.Provider>
}

export function useMoneyContext(): MoneyContextValue {
  return useContext(MoneyContext)
}
