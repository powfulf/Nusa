/*
 * Copies the exact font files Nusa ships out of the @fontsource packages and
 * into public/fonts, where they are served from Nusa's own origin.
 *
 * Fonts are self-hosted, never loaded from a third-party CDN. A font request
 * carries the reader's IP address and a timestamp to whoever serves it, and
 * for a self-hosted personal finance application that is a leak of exactly the
 * thing the user chose to self-host to avoid (CLAUDE.md §10).
 *
 * The @fontsource packages are the provenance and the update path: they stay
 * in devDependencies so `npm update` followed by `npm run fonts:sync` refreshes
 * the committed files. The files themselves are committed so that a clone can
 * build without resolving them again.
 *
 * Only the weights DESIGN.md actually specifies are copied. Shipping the eight
 * weights Plus Jakarta Sans offers when three are used would be six unused
 * files a browser might still be asked to fetch.
 */

import { copyFileSync, mkdirSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

const webRoot = fileURLToPath(new URL('..', import.meta.url))
const outDir = join(webRoot, 'public', 'fonts')

// Weights are the ones DESIGN.md § Typography names, and nothing else.
//   Plus Jakarta Sans — 500 H4, 600 H2/H3, 700 Display/H1
//   DM Sans           — 400 body, 500 captions, labels and control text
//   Fira Code         — 400 code
const FACES = [
  { pkg: 'plus-jakarta-sans', weights: [500, 600, 700] },
  { pkg: 'dm-sans', weights: [400, 500] },
  { pkg: 'fira-code', weights: [400] },
]

// Latin and latin-ext only. Indonesian and English are both covered by latin;
// latin-ext adds the accented characters European locales need. Non-Latin
// scripts will need their own subsets — see DESIGN.md § Typography.
const SUBSETS = ['latin', 'latin-ext']

mkdirSync(outDir, { recursive: true })

let copied = 0
let bytes = 0
for (const { pkg, weights } of FACES) {
  const from = join(webRoot, 'node_modules', '@fontsource', pkg, 'files')
  const available = new Set(readdirSync(from))

  for (const subset of SUBSETS) {
    for (const weight of weights) {
      const name = `${pkg}-${subset}-${weight}-normal.woff2`
      if (!available.has(name)) {
        console.error(`missing ${name} in @fontsource/${pkg}`)
        process.exit(1)
      }
      const target = join(outDir, name)
      copyFileSync(join(from, name), target)
      bytes += statSync(target).size
      copied += 1
    }
  }
}

console.log(`fonts:sync: ${copied} files, ${(bytes / 1024).toFixed(0)} kB total, into public/fonts`)
