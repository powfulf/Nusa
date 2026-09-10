/*
 * The statically checkable parts of CLAUDE.md §8.2 — the functional floor that
 * binds any design system, including a future replacement.
 *
 * DESIGN.md decides how Nusa looks and `lint:tokens` keeps that decision in one
 * place. This is a different question: it checks the things that must hold
 * whatever the design system says. Two of them can be answered by reading the
 * source; the rest need computed CSS at a real viewport, and live in the
 * Playwright suite instead.
 *
 * Run with `npm run lint:floor`.
 *
 * WHAT THIS FILE CANNOT SEE, stated so nobody reads a pass as more than it is:
 *
 *   - Computed geometry. A 44x44 touch target is a fact about layout at 360px,
 *     not about a class name being present.
 *   - Computed style. `font-variant-numeric` resolves in a browser; a class
 *     name in the source is a proxy for it.
 *   - A class name assembled across two statements. No scan short of data-flow
 *     analysis catches `const p = 'bg-'; cls = p + tone`.
 *
 * Those belong to Playwright, and the split is deliberate rather than an
 * omission.
 */

import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const webRoot = fileURLToPath(new URL('..', import.meta.url))

/*
 * Only what ships as interface. `scripts/` is Node tooling that emits no UI —
 * and this file itself names every banned property, so scanning it would make
 * the guard fail on its own source.
 */
const SCAN_DIRS = ['src']
const SCAN_FILES = ['index.html']
const SCAN_EXT = /\.(ts|tsx|css|html)$/

const problems = []

function walk(dir) {
  const out = []
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) out.push(...walk(full))
    else if (SCAN_EXT.test(entry)) out.push(full)
  }
  return out
}

const files = [
  ...SCAN_DIRS.flatMap((d) => walk(join(webRoot, d))),
  ...SCAN_FILES.map((f) => join(webRoot, f)),
]

function report(file, line, text, message) {
  problems.push(`${file}:${line}  ${message}\n    ${text.trim()}`)
}

/*
 * G1 — logical properties only.
 *
 * RTL is planned, and logical properties from the start are what keep that a
 * stylesheet change rather than a rewrite. Both halves are covered: the
 * Tailwind utility and the raw CSS declaration it compiles to.
 *
 * Comments are NOT exempt, deliberately. M1 settled this: when a rule is
 * stated as a command, the command's output is the criterion, and a check with
 * a judgement call in it stops being a check. A comment that needs to mention
 * a banned property says "the physical equivalent" instead.
 */

// Tailwind utilities that take a value after a dash: ml-4, left-0, border-l-2.
const PHYSICAL_DASHED = [
  'ml',
  'mr',
  'pl',
  'pr',
  'left',
  'right',
  'border-l',
  'border-r',
  'rounded-l',
  'rounded-r',
  'rounded-tl',
  'rounded-tr',
  'rounded-bl',
  'rounded-br',
  'scroll-ml',
  'scroll-mr',
  'scroll-pl',
  'scroll-pr',
]

// Tailwind utilities that stand alone: text-left, float-right, border-l.
const PHYSICAL_BARE = [
  'text-left',
  'text-right',
  'float-left',
  'float-right',
  'border-l',
  'border-r',
  'rounded-l',
  'rounded-r',
  'rounded-tl',
  'rounded-tr',
  'rounded-bl',
  'rounded-br',
  'clear-left',
  'clear-right',
]

// A utility may carry variant prefixes (md:, hover:, group-hover:), so the
// left boundary accepts a colon as well as whitespace and quote characters.
const BOUNDARY_START = "(?:^|[\\s'\"`{(\\[:])"
const utilityPattern = new RegExp(
  BOUNDARY_START +
    '(?:' +
    PHYSICAL_DASHED.map((u) => u + '-(?:\\d|\\[|auto|px|full|screen)') .join('|') +
    '|' +
    PHYSICAL_BARE.join('|') +
    ')' +
    "(?=$|[\\s'\"`}\\])])",
)

/*
 * Raw CSS declarations, in two patterns rather than one.
 *
 * The first names properties that cannot mean anything else — `margin-left`
 * has no other reading in any language here, so it is refused everywhere,
 * comments included. (`margin-inline-start` does not contain it, so the
 * correct spelling needs no allowance.)
 *
 * The second is `left:` and `right:` on their own, and it is confined to
 * stylesheets. Sixteen deliberate breaks passed before real code arrived and
 * showed why: `readonly left: string` on an error type is TypeScript, not CSS,
 * and flagging it would have taught the next contributor that this guard cries
 * wolf — which is worse than not having it. Nothing is lost by the narrowing,
 * because a physical offset written into a component would be an inline style,
 * and `lint:tokens` already refuses those outright.
 */
const cssPropertyPattern =
  /\b(?:margin|padding|border|scroll-margin|scroll-padding|inset)-(?:left|right)\b|\bborder-(?:top|bottom)-(?:left|right)-radius\b|text-align\s*:\s*(?:left|right)\b|float\s*:\s*(?:left|right)\b/
const cssOffsetPattern = /(?:^|[;{\s])(?:left|right)\s*:/

/*
 * G2 — no dynamically assembled class names.
 *
 * Tailwind scans source text for complete class names. A name built at runtime
 * is not in the source, so the utility is never emitted: the element is styled
 * in development, where nothing is purged, and unstyled in production. Nobody
 * running the dev server on this machine would ever see that.
 *
 * Building a class list as an array of whole literals and joining it stays
 * legal — every element is a complete name, so Tailwind sees all of them.
 */
const UTILITY_PREFIX =
  '(?:bg|text|border|rounded|outline|ring|shadow|fill|stroke|divide|from|via|to|' +
  'p|px|py|pt|pb|ps|pe|m|mx|my|mt|mb|ms|me|w|h|min-h|min-w|max-w|max-h|gap|' +
  'grid-cols|col-span|row-span|order|flex|basis|font|leading|tracking|opacity|z|' +
  'inset|top|bottom|start|end|translate|scale|rotate|duration|ease|delay|animate)'

// Values a Tailwind utility actually ends in: a numeric step, or one of this
// project's token scale names. `${id}-error` is a DOM identifier, not a class.
const UTILITY_VALUE =
  '(?:[0-9]+|content|fill|surface|primary|secondary|muted|subtle|strong|inverse|' +
  'base|sunken|default|hover|xs|sm|md|lg|xl|full|touch|row|topbar|narrow|prose|shell)'

const dynamicPrefix = new RegExp(UTILITY_PREFIX + String.raw`-\$\{`) //  `bg-${tone}`
const dynamicSuffix = new RegExp(String.raw`\}-` + UTILITY_VALUE + String.raw`\b`) // `${tone}-500`
const dynamicConcat = new RegExp(
  String.raw`['"]` + UTILITY_PREFIX + String.raw`-['"]\s*\+`, // 'bg-' + tone
)

/*
 * G6 — the money layer never reaches for a double.
 *
 * §4.1 bans float for money, and on this platform the ban is easy to breach by
 * accident: every arithmetic operator, every numeric literal and half the
 * standard library produce a `number`. The escapes below are the ones that
 * would silently convert a bigint amount into an IEEE 754 double and lose
 * integers above 2^53 — roughly Rp 90 trillion in sen.
 *
 * `Number.isInteger` and friends are permitted: they inspect a value without
 * converting one. `Number(` is the conversion, and it is not.
 *
 * This cannot prove the absence of a double — `a / b` on two numbers produces
 * one and no scan will catch that shape. It closes the doors somebody walks
 * through on purpose, and the property tests close the rest by comparing
 * against exact integers past the boundary where a double stops counting.
 */
const MONEY_DIR = 'src/money/'
const doublePattern =
  /\bparseFloat\s*\(|\bparseInt\s*\(|(?<![.\w])Number\s*\(|\.toFixed\s*\(|\.toPrecision\s*\(|\bMath\s*\./

let moneyFilesSeen = 0

for (const absolute of files) {
  const file = relative(webRoot, absolute).split(sep).join('/')
  const source = readFileSync(absolute, 'utf8')
  const lines = source.split(/\r?\n/)

  if (file.startsWith(MONEY_DIR)) {
    moneyFilesSeen += 1
    lines.forEach((text, index) => {
      if (doublePattern.test(text)) {
        report(file, index + 1, text, 'the money layer must never convert through a double (§4.1)')
      }
    })
  }

  lines.forEach((text, index) => {
    const line = index + 1

    if (/\.(tsx?|html)$/.test(file) && utilityPattern.test(text)) {
      report(file, line, text, 'physical direction utility — use the logical equivalent')
    }
    if (cssPropertyPattern.test(text)) {
      report(file, line, text, 'physical CSS property — use the logical equivalent')
    }
    if (/[.]css$/.test(file) && cssOffsetPattern.test(text)) {
      report(file, line, text, 'physical CSS offset — use the logical equivalent')
    }
    if (/\.tsx?$/.test(file) && dynamicConcat.test(text)) {
      report(file, line, text, 'class name built by concatenation — Tailwind will not emit it')
    }
  })

  // Template literals are matched across the whole file rather than per line,
  // because one can span lines. Nested template literals are not handled, and
  // there are none here.
  if (/\.tsx?$/.test(file)) {
    for (const match of source.matchAll(/`[^`]*`/g)) {
      const literal = match[0]
      if (dynamicPrefix.test(literal) || dynamicSuffix.test(literal)) {
        const line = source.slice(0, match.index).split(/\r?\n/).length
        report(file, line, literal, 'class name built by interpolation — Tailwind will not emit it')
      }
    }
  }
}

/*
 * A check that reads nothing passes. This has cost this project five times, so
 * the guard states how much it read and refuses to succeed on an empty set.
 */
if (files.length === 0) {
  console.error('\nlint:floor read no files at all. The scan roots are wrong.\n')
  process.exit(1)
}

/*
 * The money check filters, so it says how many files got through the filter
 * rather than only that it was handed some. A filter matching nothing is
 * indistinguishable from one matching everything correctly, and that has cost
 * this project a whole class of guard before.
 */
if (moneyFilesSeen === 0) {
  console.error(`\nlint:floor found no files under ${MONEY_DIR} — the money check read nothing.\n`)
  process.exit(1)
}

if (problems.length > 0) {
  console.error(`\nlint:floor found ${problems.length} problem(s):\n`)
  for (const p of problems) console.error(`  ${p}\n`)
  console.error('CLAUDE.md §8.2 binds any design system. Logical properties keep RTL a')
  console.error('stylesheet change; complete class names keep production matching dev.\n')
  process.exit(1)
}

console.log(`lint:floor: clean (${files.length} files, ${moneyFilesSeen} of them in the money layer)`)
