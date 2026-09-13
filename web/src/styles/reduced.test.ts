import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

/*
 * DESIGN.md § Motion: the system's reduced-motion preference and the "reduce
 * visual effects" toggle combine as a UNION, and neither ever restores what
 * the other removed. That is stated as a rule about the two declarations, not
 * as a consequence of stylesheet order — and this is what makes it one. Each
 * block may only set a duration to zero (and the toggle's block may flatten
 * elevation); a block that set a duration back to anything else would make
 * the outcome depend on which block came last, which is precisely the
 * accident the rule forbids.
 *
 * Read from tokens.css directly: jsdom applies no media queries, so the only
 * way to check the two blocks is to read them.
 */
const css = readFileSync(join(__dirname, 'tokens.css'), 'utf8')

function block(opener: RegExp): string {
  const start = css.search(opener)
  expect(start, `block ${opener} exists`).toBeGreaterThanOrEqual(0)
  let depth = 0
  for (let i = css.indexOf('{', start); i < css.length; i += 1) {
    if (css[i] === '{') depth += 1
    if (css[i] === '}') {
      depth -= 1
      if (depth === 0) return css.slice(start, i + 1)
    }
  }
  throw new Error('unbalanced block')
}

const declarations = (text: string) =>
  [...text.matchAll(/(--[a-z0-9-]+)\s*:\s*([^;]+);/g)].map((m) => [m[1] ?? '', (m[2] ?? '').trim()] as const)

describe('reduced motion and reduced effects', () => {
  const system = declarations(block(/@media \(prefers-reduced-motion: reduce\)/))
  const toggle = declarations(block(/:root\[data-effects='reduced'\]/))
  const base = declarations(css.slice(0, css.search(/@media|\[data-/)))

  it('found both blocks and the base values, so the comparison below is not over nothing', () => {
    expect(system.length).toBeGreaterThan(0)
    expect(toggle.length).toBeGreaterThan(0)
    const baseMotion = base.filter(([name]) => name.startsWith('--motion-'))
    expect(baseMotion.length).toBeGreaterThan(0)
    for (const [name, value] of baseMotion) {
      expect(value, `${name} is non-zero at rest, or zeroing it would prove nothing`).not.toBe('0ms')
    }
  })

  it('lets the system preference stop motion and touch nothing else', () => {
    for (const [name, value] of system) {
      expect(name, 'only motion tokens').toMatch(/^--motion-/)
      expect(value, `${name} collapses to zero`).toBe('0ms')
    }
    const motionNames = base.filter(([n]) => n.startsWith('--motion-')).map(([n]) => n).sort()
    expect(system.map(([n]) => n).sort(), 'every motion token, not a subset').toEqual(motionNames)
  })

  it('lets the toggle stop motion and flatten elevation, and never restores either', () => {
    for (const [name, value] of toggle) {
      if (name.startsWith('--motion-')) expect(value, name).toBe('0ms')
      else if (name.startsWith('--elevation-')) expect(value, name).toBe('0 0 0 1px var(--border-subtle)')
      else throw new Error(`${name}: the toggle changes motion and elevation only — colour, spacing and type are untouched`)
    }
    const motionNames = base.filter(([n]) => n.startsWith('--motion-')).map(([n]) => n).sort()
    const elevationNames = base.filter(([n]) => n.startsWith('--elevation-')).map(([n]) => n).sort()
    expect(toggle.filter(([n]) => n.startsWith('--motion-')).map(([n]) => n).sort()).toEqual(motionNames)
    expect(toggle.filter(([n]) => n.startsWith('--elevation-')).map(([n]) => n).sort()).toEqual(elevationNames)
  })
})
