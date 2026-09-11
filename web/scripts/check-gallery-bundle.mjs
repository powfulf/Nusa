/*
 * Proves the component gallery is not in the production bundle.
 *
 * "Non-production" is a claim about our own code, and §11 makes such a claim a
 * candidate for testing rather than a premise. The gallery route is registered
 * behind `import.meta.env.DEV` and imported lazily; Rollup should therefore
 * drop the module entirely. Should. This reads the output and finds out.
 *
 * Two builds, and the second is what keeps the first honest. A production
 * build must NOT contain the marker; a development-mode build MUST. Without
 * the second assertion a marker nobody exports any more, a renamed constant,
 * or a build that silently emitted nothing would all read as "not in the
 * bundle" — a comparison whose one side is empty passes by reading nothing.
 *
 * Only `.js` under dist/assets is read. Source maps carry the source text by
 * design and would contain the marker in any build.
 *
 * Run with `npm run check:gallery`.
 */

import { execSync } from 'node:child_process'
import { readdirSync, readFileSync, rmSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

const webRoot = fileURLToPath(new URL('..', import.meta.url))
const MARKER = 'nusa-gallery-marker-7f3a2c'

function build(mode) {
  rmSync(join(webRoot, 'dist'), { recursive: true, force: true })
  execSync(`npx vite build --mode ${mode} --logLevel error`, { cwd: webRoot, stdio: 'inherit' })
  const assets = join(webRoot, 'dist', 'assets')
  const files = readdirSync(assets).filter((f) => f.endsWith('.js'))
  if (files.length === 0) throw new Error(`${mode} build produced no .js under dist/assets`)
  let hits = 0
  let bytes = 0
  for (const f of files) {
    const text = readFileSync(join(assets, f), 'utf8')
    bytes += statSync(join(assets, f)).size
    if (text.includes(MARKER)) hits += 1
  }
  return { files: files.length, bytes, hits }
}

const prod = build('production')
const dev = build('development')

console.log(
  `check:gallery: production ${prod.files} chunk(s), ${prod.bytes} bytes, marker in ${prod.hits}; ` +
    `development ${dev.files} chunk(s), ${dev.bytes} bytes, marker in ${dev.hits}`,
)

const problems = []
if (prod.hits > 0) problems.push('the gallery is in the PRODUCTION bundle')
if (dev.hits === 0) {
  problems.push(
    'the gallery is not in the DEVELOPMENT bundle either — the marker is not being emitted, ' +
      'so the production check above proves nothing',
  )
}

// Leave a production build behind, which is what everything downstream expects.
build('production')

if (problems.length > 0) {
  console.error('\ncheck:gallery FAILED:')
  for (const p of problems) console.error(`  - ${p}`)
  process.exit(1)
}
