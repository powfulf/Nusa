/*
 * Guards that keep DESIGN.md the only source of visual decisions.
 *
 * Tailwind fails silently on an unknown colour class — `bg-red-500` with the
 * palette replaced simply emits no rule — so a config change alone is not
 * enough. These checks look for the raw values a stray decision would need.
 *
 * Run with `npm run lint:tokens`.
 */

import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const webRoot = fileURLToPath(new URL('..', import.meta.url))

// tokens.css is the one file allowed to hold raw values; fonts.css will hold
// the @font-face declarations that name the families. tailwind.config.js is
// allowed breakpoints only, because a media query cannot read a custom
// property — that narrow exception is checked separately below.
const TOKENS_FILE = join('src', 'styles', 'tokens.css')
const FONTS_FILE = join('src', 'styles', 'fonts.css')
const TAILWIND_CONFIG = 'tailwind.config.js'

const SCAN_DIRS = ['src']
const SCAN_FILES = ['tailwind.config.js', 'index.html', 'postcss.config.js', 'vite.config.ts']
const SCAN_EXT = /\.(ts|tsx|css|html|js)$/

const problems = []

function walk(dir) {
  const out = []
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      out.push(...walk(full))
    } else if (SCAN_EXT.test(entry)) {
      out.push(full)
    }
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

for (const absolute of files) {
  const file = relative(webRoot, absolute).split(sep).join('/')
  const isTokens = relative(webRoot, absolute) === TOKENS_FILE
  const isFonts = relative(webRoot, absolute) === FONTS_FILE
  const isTailwind = relative(webRoot, absolute) === TAILWIND_CONFIG
  const lines = readFileSync(absolute, 'utf8').split(/\r?\n/)

  lines.forEach((text, index) => {
    const line = index + 1

    // 1. Raw colours. Hex, rgb()/rgba(), hsl()/hsla() and the CSS named
    //    colours that actually paint something.
    if (!isTokens) {
      if (/#[0-9a-fA-F]{3,8}\b/.test(text) && !/^\s*(\/\/|\*|<!--)/.test(text)) {
        report(file, line, text, 'raw colour — colours live only in tokens.css')
      }
      if (/\b(rgba?|hsla?)\s*\(/.test(text)) {
        report(file, line, text, 'raw colour function — colours live only in tokens.css')
      }
    }

    // 2. Font families. A declaration whose value is only a var() reference is
    //    a binding, not a decision, so it is allowed — what is forbidden is a
    //    literal family name anywhere but tokens.css.
    if (!isTokens && !isFonts) {
      const decl = text.match(/(?:font-family|fontFamily)\s*:\s*(.*)$/)
      if (decl) {
        const value = decl[1].trim()
        const isBinding = value === '' || value === '{' || /^['"]?var\(--[a-z0-9-]+\)['"]?,?$/.test(value)
        if (!isBinding) {
          report(file, line, text, 'literal font family — typefaces live only in tokens.css')
        }
      }
      if (/['"](Plus Jakarta Sans|DM Sans|Fira Code)['"]/.test(text)) {
        report(file, line, text, 'font name — typefaces live only in tokens.css')
      }
    }

    // 2b. Fonts are self-hosted, always. A CDN request hands the reader's IP
    //     address and a timestamp to a third party, which is precisely what
    //     someone running Nusa on their own hardware chose to avoid
    //     (CLAUDE.md §10). This applies to fonts.css too — there is no file
    //     where reaching for a font CDN is acceptable.
    if (/fonts\.googleapis\.com|fonts\.gstatic\.com|use\.typekit|cdn\.jsdelivr|unpkg\.com/.test(text)) {
      report(file, line, text, 'third-party asset host — everything is served from our own origin')
    }

    // 3. Literal lengths written as Tailwind arbitrary values. Spacing, sizing
    //    and type all come from named token scales.
    //
    //    The whole line is scanned rather than just a className attribute:
    //    class strings are also built as arrays and joined, and an earlier
    //    version of this check matched only the attribute form and quietly
    //    missed both `gap-[13px]` and every array-built className. The
    //    bracketed-length pattern is specific enough to Tailwind that scanning
    //    broadly costs nothing.
    if (/\.tsx?$/.test(file) || /\.html$/.test(file)) {
      if (/\[-?\d*\.?\d+(px|rem|em|pt|vh|vw)\]/.test(text)) {
        report(file, line, text, 'arbitrary length — use a token scale')
      }
      if (/style=\{\{/.test(text)) {
        report(file, line, text, 'inline style — visual values belong to tokens')
      }
    }

    // 4. tailwind.config.js may hold breakpoints and nothing else raw.
    if (isTailwind) {
      const raw = text.match(/'(\d+)px'/)
      if (raw && !/^\s*('?(sm|md|lg|xl|2xl)'?)\s*:/.test(text)) {
        report(file, line, text, 'raw length outside the screens block')
      }
    }
  })
}

// 5. Every custom property tokens.css defines must be referenced somewhere, and
//    every var() used must be defined. A typo in a var() name is invisible in
//    the browser: the property simply does not apply.
const tokensSource = readFileSync(join(webRoot, TOKENS_FILE), 'utf8')
const defined = new Set([...tokensSource.matchAll(/^\s*(--[a-z0-9-]+)\s*:/gm)].map((m) => m[1]))

const consumers = files.filter((f) => relative(webRoot, f) !== TOKENS_FILE)
const used = new Set()
for (const absolute of consumers) {
  for (const m of readFileSync(absolute, 'utf8').matchAll(/var\(\s*(--[a-z0-9-]+)/g)) {
    used.add(m[1])
  }
}
for (const m of tokensSource.matchAll(/var\(\s*(--[a-z0-9-]+)/g)) used.add(m[1])

for (const name of used) {
  if (!defined.has(name)) {
    problems.push(`unknown token ${name} — not defined in ${TOKENS_FILE}`)
  }
}

if (problems.length > 0) {
  console.error(`\nlint:tokens found ${problems.length} problem(s):\n`)
  for (const p of problems) console.error(`  ${p}\n`)
  console.error('Visual values are decided in DESIGN.md and transcribed into')
  console.error(`${TOKENS_FILE}. They do not live anywhere else.\n`)
  process.exit(1)
}

console.log(`lint:tokens: clean (${files.length} files, ${defined.size} tokens defined)`)
