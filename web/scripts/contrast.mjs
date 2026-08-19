/*
 * Contrast verification against the thresholds in DESIGN.md § Accessibility
 * floor.
 *
 * Thresholds differ by role. A single uniform number produces both false
 * failures (a 24px heading held to 4.5:1) and false passes (a 12px chip label
 * held to 3:1), so each pairing declares what it is:
 *
 *   text        4.5:1   normal text, under 18.66px bold / 24px   WCAG 1.4.3
 *   large-text  3:1     18.66px bold and up, or 24px and up      WCAG 1.4.3
 *   icon        3:1     icons and marks that carry meaning       WCAG 1.4.11
 *   boundary    3:1     a boundary that is a control's sole cue  WCAG 1.4.11
 *   focus       3:1     focus indicators, unconditional          WCAG 1.4.11
 *
 * Values are read from tokens.css rather than repeated here, so this cannot
 * drift from what actually ships.
 */

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const tokensPath = fileURLToPath(new URL('../src/styles/tokens.css', import.meta.url))
const source = readFileSync(tokensPath, 'utf8')

// Only the :root block, so the reduced-effects and dense overrides below it do
// not shadow the real palette.
const rootBlock = source.slice(source.indexOf(':root {'), source.indexOf('\n}\n'))
const tokens = new Map(
  [...rootBlock.matchAll(/^\s*(--[a-z0-9-]+)\s*:\s*([^;]+);/gm)].map((m) => [m[1], m[2].trim()]),
)

const THRESHOLDS = { text: 4.5, 'large-text': 3, icon: 3, boundary: 3, focus: 3 }

/*
 * A pairing that clears its threshold by a hair is one token tweak away from
 * failing, and nothing about "4.58" announces that on its way past. These
 * bands report a pass with a warning instead of silence, so whoever next edits
 * a colour can see which pairings have no room left. Warnings never fail the
 * build — they are information, not a second, secret threshold.
 */
const WARN_BELOW = { 4.5: 4.8, 3: 3.3 }

function parse(token) {
  const raw = tokens.get(token)
  if (raw === undefined) throw new Error(`token ${token} is not defined in tokens.css`)
  const hex = raw.replace('#', '')
  if (hex.length === 8) {
    // 8-digit hex: an alpha value, composited over the surface it sits on.
    return {
      rgb: [0, 2, 4].map((i) => parseInt(hex.slice(i, i + 2), 16)),
      alpha: parseInt(hex.slice(6, 8), 16) / 255,
    }
  }
  if (hex.length !== 6) throw new Error(`token ${token} is not a solid colour: ${raw}`)
  return { rgb: [0, 2, 4].map((i) => parseInt(hex.slice(i, i + 2), 16)), alpha: 1 }
}

const channel = (c) => {
  const s = c / 255
  return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4)
}
const luminance = ([r, g, b]) => 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b)

function ratio(fgToken, bgToken) {
  const bg = parse(bgToken)
  if (bg.alpha !== 1) throw new Error(`background ${bgToken} is translucent`)
  const fg = parse(fgToken)
  // A translucent foreground is composited; a translucent *surface* is not
  // allowed at all, which is why only this direction is handled.
  const rgb = fg.alpha === 1 ? fg.rgb : fg.rgb.map((c, i) => c * fg.alpha + bg.rgb[i] * (1 - fg.alpha))
  const [hi, lo] = [luminance(rgb), luminance(bg.rgb)].sort((a, b) => b - a)
  return (hi + 0.05) / (lo + 0.05)
}

// Surfaces any text may legitimately land on.
const READABLE_SURFACES = [
  '--surface-default',
  '--surface-base',
  '--surface-sunken',
  '--status-brand-surface',
  '--status-success-surface',
  '--status-warning-surface',
  '--status-error-surface',
  '--status-info-surface',
]

const pairs = []
const add = (fg, bg, role, note) => pairs.push({ fg, bg, role, note })

// Neutral text, checked against every surface it can appear on.
for (const surface of READABLE_SURFACES) {
  add('--text-primary', surface, 'text', 'body and numerals')
  add('--text-secondary', surface, 'text', 'supporting text')
  add('--text-muted', surface, 'text', 'helper and descriptions')
}
add('--text-on-inverse', '--surface-inverse', 'text', 'tooltip and tinted header')

// Status CONTENT values: on the neutral surfaces and on their own chip.
const STATUSES = ['brand', 'success', 'warning', 'error', 'info']
for (const s of STATUSES) {
  for (const surface of ['--surface-default', '--surface-base', '--surface-sunken']) {
    add(`--status-${s}-content`, surface, 'text', `${s} text`)
  }
  add(`--status-${s}-content`, `--status-${s}-surface`, 'text', `${s} chip label`)
  add(`--status-${s}-content`, '--surface-default', 'icon', `${s} icon carrying meaning`)
}

// Control fills carrying a label.
add('--control-primary-text', '--control-primary-fill', 'text', 'primary button label')
add('--control-primary-text', '--control-primary-hover', 'text', 'primary button hover')
add('--control-primary-text', '--control-destructive-fill', 'text', 'destructive button label')
add('--control-primary-text', '--control-destructive-hover', 'text', 'destructive button hover')

// Focus, unconditional, including the dark fills the inverse ring exists for.
add('--focus-ring', '--surface-default', 'focus', 'focus ring on a card')
add('--focus-ring', '--surface-base', 'focus', 'focus ring on the page')
add('--focus-ring', '--surface-sunken', 'focus', 'focus ring on a sunken fill')
add('--focus-ring-inverse', '--control-primary-fill', 'focus', 'focus ring on a dark fill')
add('--focus-ring-inverse', '--surface-inverse', 'focus', 'focus ring on an inverse surface')
add('--focus-ring-inverse', '--control-destructive-fill', 'focus', 'focus ring on destructive')

/*
 * Only Display (40/32px bold) and H1 (32/28px bold) are large text. H2 is
 * semibold and drops to 22px on mobile, and H3 is 20px — both are under the
 * 18.66px-bold / 24px line, so they are normal text and are already covered by
 * the --text-primary loop above at 4.5:1.
 *
 * Self-hosting the fonts changed the optical size of headings but not their
 * declared size, so nothing moved between these categories.
 */
add('--text-primary', '--surface-base', 'large-text', 'Display and H1 only')

const results = pairs.map((p) => {
  const value = ratio(p.fg, p.bg)
  const need = THRESHOLDS[p.role]
  const warnBelow = WARN_BELOW[need]
  const pass = value >= need
  return { ...p, value, need, pass, thin: pass && value < warnBelow, warnBelow }
})

const failures = results.filter((r) => !r.pass)
const thin = results.filter((r) => r.thin)
const verbose = process.argv.includes('--verbose') || failures.length > 0

if (verbose) {
  const width = Math.max(...results.map((r) => r.fg.length))
  for (const r of results) {
    const mark = !r.pass ? 'FAIL' : r.thin ? 'warn' : 'ok  '
    console.log(
      `${mark} ${r.value.toFixed(2).padStart(6)} (need ${String(r.need).padEnd(3)}) ` +
        `${r.fg.padEnd(width)} on ${r.bg.padEnd(width)}  ${r.role}/${r.note}`,
    )
  }
  console.log()
}

if (failures.length > 0) {
  console.error(`contrast: ${failures.length} of ${results.length} pairings fail.\n`)
  console.error('Do not adjust a value here to make this pass. The values come from')
  console.error('DESIGN.md; fix them there and re-derive tokens.css.\n')
  process.exit(1)
}

const worst = results.reduce((a, b) => (a.value < b.value ? a : b))
console.log(
  `contrast: ${results.length} pairings pass. ` +
    `Tightest ${worst.value.toFixed(2)}:1 (need ${worst.need}) — ${worst.fg} on ${worst.bg}.`,
)

if (thin.length > 0) {
  console.log(
    `\ncontrast: ${thin.length} pairing(s) pass with little room left. Not a failure,` +
      ` but treat these as load-bearing when editing a colour:`,
  )
  for (const r of thin.sort((a, b) => a.value - b.value)) {
    console.log(
      `  ${r.value.toFixed(2)}:1 (need ${r.need}, comfortable at ${r.warnBelow}) ` +
        `${r.fg} on ${r.bg} — ${r.note}`,
    )
  }
}
