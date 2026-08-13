/// <reference types="vitest/config" />
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import process from 'node:process'

import en from './src/i18n/en.json'

/**
 * Fills %APP_NAME% in index.html from the message catalog.
 *
 * The product name is a codename and will change. Keeping the document title
 * sourced from the catalog means it changes in one place, and no user-facing
 * string is ever hardcoded in markup.
 */
function appNamePlugin(): Plugin {
  return {
    name: 'app-name-from-catalog',
    transformIndexHtml(html) {
      return html.replaceAll('%APP_NAME%', en.app.name)
    },
  }
}

// During development Vite serves the app and forwards API calls to the Go
// server, so the browser sees a single origin and no CORS setup is needed.
// In production the Go server serves the built assets itself.
const apiTarget = process.env.NUSA_API_PROXY_TARGET ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react(), appNamePlugin()],
  server: {
    port: 5173,
    // Listen on all interfaces so the dev server is reachable from a container.
    host: true,
    proxy: {
      '/healthz': { target: apiTarget, changeOrigin: true },
      '/api': { target: apiTarget, changeOrigin: true },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    css: false,
  },
})
