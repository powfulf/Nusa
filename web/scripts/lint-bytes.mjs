/*
 * Refuses invisible characters in any text file in the repository.
 *
 * This machine substitutes raw control characters for what was written
 * whenever text passes through a shell, and it does so silently: the file
 * compiles, the tests pass, and the byte is invisible in every diff. It has now
 * happened four times here — six 0x1F bytes in `internal/api/cursor_test.go`
 * during M2b Phase 2, and in M3 a 0x1F in `src/money/format.ts`, three more in
 * `src/i18n/icu.ts`, and one in the paragraph of `CLAUDE.md` describing the
 * problem.
 *
 * Every one of those was caught by running a two-line scan from memory. That is
 * the part this file exists to fix: a check that runs when somebody remembers
 * is a check that stops running the week they are busy. It scans the whole
 * repository rather than only `web/`, because the Go sources, the migrations
 * and the normative documents are where three of the four landed.
 *
 * What it refuses:
 *
 *   - C0 control characters other than tab, newline and carriage return.
 *   - A UTF-8 BOM. M0's `.gitignore` was UTF-16 and git read none of it; a BOM
 *     is the same class of problem in a friendlier disguise.
 *   - A lone carriage return, which is neither a Windows line ending nor a Unix
 *     one and comes from an editor that got confused.
 *
 * A CRLF file is NOT refused: `.gitattributes` pins `eol=lf` in the repository,
 * and the working tree on Windows is legitimately CRLF. Refusing it here would
 * fire on every file on this machine and teach everybody to skip the check.
 *
 * Run with `npm run lint:bytes`.
 */

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = fileURLToPath(new URL('../..', import.meta.url))

const SKIP_DIRS = new Set(['.git', 'node_modules', 'dist', 'bin', 'coverage', '.dev'])

// An allowlist rather than a denylist of binary types: a file extension nobody
// anticipated should be skipped and reported, never scanned as text and
// reported as full of control characters.
const TEXT_EXT =
  /\.(ts|tsx|js|mjs|cjs|json|css|html|md|go|sql|ya?ml|toml|txt|sh|env|example|gitignore|gitattributes|dockerignore|mod|sum)$|^(Makefile|Dockerfile|LICENSE|CONTRIBUTING|SECURITY)$/

function walk(dir) {
  const out = []
  for (const entry of readdirSync(dir)) {
    if (SKIP_DIRS.has(entry)) continue
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) out.push(...walk(full))
    else if (TEXT_EXT.test(entry)) out.push(full)
  }
  return out
}

const files = walk(repoRoot)
const problems = []

for (const absolute of files) {
  const name = relative(repoRoot, absolute).split(sep).join('/')
  const bytes = readFileSync(absolute)

  if (bytes.length >= 3 && bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf) {
    problems.push(`${name}  starts with a UTF-8 BOM`)
  }

  let line = 1
  for (let i = 0; i < bytes.length; i += 1) {
    const b = bytes[i]
    if (b === 0x0a) {
      line += 1
      continue
    }
    if (b === 0x09) continue
    if (b === 0x0d) {
      // A carriage return is only acceptable immediately before a newline.
      if (bytes[i + 1] !== 0x0a) problems.push(`${name}:${line}  lone carriage return (0x0D)`)
      continue
    }
    if (b < 0x20 || b === 0x7f) {
      const hex = b.toString(16).padStart(2, '0')
      problems.push(
        `${name}:${line}  invisible control character 0x${hex} — write it as an escape`,
      )
    }
  }
}

/*
 * A check that filters must say how many items got through the filter, not
 * merely that it was handed some. `files.length > 0` would still be satisfied
 * by a scan that had quietly stopped reading everything except `.json`.
 *
 * The three categories below are named because they are where this bug has
 * actually landed: Go (`cursor_test.go`), TypeScript (`format.ts`, `icu.ts`)
 * and Markdown (`CLAUDE.md`). A scan that stops covering one of them is the
 * failure mode worth refusing by name.
 */
const seen = {
  go: files.filter((f) => f.endsWith('.go')).length,
  ts: files.filter((f) => /\.tsx?$/.test(f)).length,
  md: files.filter((f) => f.endsWith('.md')).length,
}
const missing = Object.entries(seen).filter(([, n]) => n === 0)
if (files.length === 0 || missing.length > 0) {
  console.error('\nlint:bytes is not reading what it claims to read.')
  console.error(`  files: ${files.length}, go: ${seen.go}, ts: ${seen.ts}, md: ${seen.md}`)
  console.error('The scan root, the skip list or the extension list is wrong.\n')
  process.exit(1)
}

if (problems.length > 0) {
  console.error(`\nlint:bytes found ${problems.length} problem(s):\n`)
  for (const p of problems.slice(0, 40)) console.error(`  ${p}`)
  if (problems.length > 40) console.error(`  ... and ${problems.length - 40} more`)
  console.error('\nText passing through a shell on this machine loses escapes silently.')
  console.error('Write the character as an escape, or write the file with an editor tool.\n')
  process.exit(1)
}

console.log(
  `lint:bytes: clean (${files.length} text files — ${seen.go} go, ${seen.ts} ts, ${seen.md} md)`,
)
