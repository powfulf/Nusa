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

/*
 * A component is a function — or a forwardRef / memo wrapper, which is a plain
 * object carrying React's $$typeof tag. The first version of this guard tested
 * `typeof value === 'function'` only, and reported two missing entries when
 * three primitives had none: <Input> is a forwardRef, and the guard could not
 * see it. A false NEGATIVE in an enumerating guard is the silent kind — it
 * does not cry wolf, it simply never notices the wolf.
 */
function isComponent(value: unknown): boolean {
  if (typeof value === 'function') return true
  return (
    typeof value === 'object' &&
    value !== null &&
    '$$typeof' in value &&
    typeof (value as { $$typeof: unknown }).$$typeof === 'symbol'
  )
}

/*
 * The independent source of truth the §11 enumeration rule demands.
 *
 * The runtime enumeration above can be blind — it was, to forwardRef — and no
 * break finds blindness, because a break is written in the guard's own
 * vocabulary. So the same files are read a second way, as text, and every
 * PascalCase `export function` / `export const` is collected without any
 * opinion about what it is. The two views must agree. When they do not, one of
 * them is not seeing something, and the test names it rather than picking a
 * side.
 *
 * A PascalCase export that is genuinely not a component — a constant, a
 * config object — is declared here by name. That is the reconciliation being
 * forced: it cannot be quietly ignored by either view.
 */
const sources = import.meta.glob<string>(
  ['../components/*.tsx', '!../components/*.test.tsx'],
  { eager: true, query: '?raw', import: 'default' },
)

const KNOWN_NOT_COMPONENTS: ReadonlySet<string> = new Set<string>([])

function staticallyExported(): Set<string> {
  const names = new Set<string>()
  for (const text of Object.values(sources)) {
    for (const m of text.matchAll(/^export (?:function|const) ([A-Z][A-Za-z0-9]*)\b/gm)) {
      if (m[1] !== undefined) names.add(m[1])
    }
  }
  return names
}

/** Component names actually exported from src/components, by file. */
function discovered(): Map<string, string> {
  const found = new Map<string, string>()
  for (const [file, mod] of Object.entries(modules)) {
    for (const [name, value] of Object.entries(mod)) {
      if (isComponent(value) && isComponentName(name)) found.set(name, file)
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

  it('sees, at runtime, every PascalCase export the source text declares', () => {
    const runtime = new Set(discovered().keys())
    const declared = staticallyExported()
    expect(declared.size, 'the static scan read nothing').toBeGreaterThan(0)

    const unseen = [...declared].filter((n) => !runtime.has(n) && !KNOWN_NOT_COMPONENTS.has(n))
    const unexpected = [...runtime].filter((n) => !declared.has(n))
    const stale = [...KNOWN_NOT_COMPONENTS].filter((n) => !declared.has(n) || runtime.has(n))

    expect(unseen, 'seen by the static scan but not the runtime enumeration').toEqual([])
    expect(unexpected, 'enumerated at runtime but not declared in source').toEqual([])
    expect(stale, 'listed as not-a-component but missing, or actually a component').toEqual([])
  })

  it('names a message key for every entry heading', () => {
    for (const [name, entry] of Object.entries(entries)) {
      expect(entry.titleKey, name).toMatch(/^gallery\.entry\./)
    }
  })
})
