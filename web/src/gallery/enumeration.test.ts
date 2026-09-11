import { describe, expect, it } from 'vitest'

import { entries } from './entries'
import { REQUIRED_STATES } from './registry'

/*
 * Every primitive has a complete gallery entry, and every entry names a
 * primitive that exists. Both directions, because a table that drifts out of
 * step with what it describes is an enumerating guard covering something other
 * than the thing (the route-table lesson from M2b Phase 2).
 *
 * Discovery is at RUNTIME through import.meta.glob rather than by scanning
 * source text. The §11 two-lists rule is the reason: a regex for
 * `export function X` has a second list to get wrong — type exports, hooks,
 * helpers, aliases in tests — and each of those is a false positive waiting
 * for real code to find it. At runtime a type export does not exist, a hook is
 * camelCase, and a test file is not in the glob. The second list is handled by
 * the language rather than by a pattern.
 *
 * THE SECOND LIST, so that the corpus run means something:
 *   - `*.test.tsx`             excluded from the glob
 *   - `export type` / interface  absent at runtime
 *   - hooks and helpers        camelCase, filtered by name
 *   - MoneyProvider            lives in src/money/, not src/components/
 *   - the gallery itself       lives in src/gallery/
 *   - `Money as MoneyComponent` an alias inside a test file, which is excluded
 */

// Eager so the modules are loaded and their exports inspectable now.
const modules = import.meta.glob<Record<string, unknown>>(
  ['../components/*.tsx', '!../components/*.test.tsx'],
  { eager: true },
)

const isComponentName = (name: string) => /^[A-Z][A-Za-z0-9]*$/.test(name)

/** Component names actually exported from src/components, by file. */
function discovered(): Map<string, string> {
  const found = new Map<string, string>()
  for (const [file, mod] of Object.entries(modules)) {
    for (const [name, value] of Object.entries(mod)) {
      if (typeof value === 'function' && isComponentName(name)) found.set(name, file)
    }
  }
  return found
}

describe('the gallery enumerates every primitive', () => {
  it('found something to enumerate', () => {
    // A check that filters must say how many got through the filter. A glob
    // that matched no files, or files that exported nothing PascalCase, would
    // make every assertion below vacuously true.
    expect(Object.keys(modules).length).toBeGreaterThan(0)
    expect(discovered().size).toBeGreaterThan(0)
    expect(Object.keys(entries).length).toBeGreaterThan(0)
  })

  it('has an entry for every exported primitive', () => {
    const missing = [...discovered().entries()]
      .filter(([name]) => !(name in entries))
      .map(([name, file]) => `${name} (${file})`)
    expect(missing, 'primitives with no gallery entry').toEqual([])
  })

  it('has no entry for a primitive that does not exist', () => {
    const known = discovered()
    const stale = Object.keys(entries).filter((name) => !known.has(name))
    expect(stale, 'gallery entries with no component behind them').toEqual([])
  })

  it('accounts for every required state on every entry', () => {
    const problems: string[] = []
    for (const [name, entry] of Object.entries(entries)) {
      for (const state of REQUIRED_STATES) {
        const spec = (entry.states as Record<string, unknown>)[state]
        if (spec === undefined) {
          problems.push(`${name}.${state}: missing`)
          continue
        }
        const s = spec as Record<string, unknown>
        const kinds = ['render', 'interactive', 'notApplicable'].filter((k) => k in s)
        if (kinds.length !== 1) {
          problems.push(`${name}.${state}: must be exactly one of render / interactive / notApplicable`)
          continue
        }
        if ('notApplicable' in s && !(typeof s.notApplicable === 'string' && s.notApplicable.trim())) {
          problems.push(`${name}.${state}: notApplicable needs a reason`)
        }
        if ('render' in s && typeof s.render !== 'function') {
          problems.push(`${name}.${state}: render must be a function`)
        }
      }
    }
    expect(problems).toEqual([])
  })

  it('names a message key for every entry heading', () => {
    for (const [name, entry] of Object.entries(entries)) {
      expect(entry.titleKey, name).toMatch(/^gallery\.entry\./)
    }
  })
})
