/**
 * Tailwind reads the design tokens through CSS custom properties rather than
 * holding literal colours, so a theme switch is a change of custom properties
 * and never a rebuild of utility classes.
 *
 * Token names are theme-agnostic on purpose. Light mode is planned; nothing
 * here is named "dark".
 *
 * @type {import('tailwindcss').Config}
 */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        // Base
        'bg-deep': 'var(--bg-deep)',

        // Surfaces. Glass carries blur and gloss; data surfaces are opaque and
        // high contrast, because text on a translucent surface has
        // unpredictable contrast.
        'surface-glass': 'var(--surface-glass)',
        'surface-data': 'var(--surface-data)',
        'surface-raised': 'var(--surface-raised)',
        bevel: 'var(--border-bevel)',

        // Accents
        accent: 'var(--accent)',
        'accent-alt': 'var(--accent-alt)',
        warn: 'var(--warn)',
        danger: 'var(--danger)',

        // Foreground
        'fg-primary': 'var(--text-primary)',
        'fg-secondary': 'var(--text-secondary)',
        'fg-muted': 'var(--text-muted)',
      },
      backgroundImage: {
        ambient: 'var(--bg-ambient)',
        // Gloss is an edge highlight, never a full-card gradient.
        gloss: 'var(--gloss)',
      },
      borderRadius: {
        card: 'var(--radius-card)',
        button: 'var(--radius-button)',
        input: 'var(--radius-input)',
      },
      boxShadow: {
        // Elevation is a soft glow in the accent hue, not a hard drop shadow.
        glow: 'var(--elevation-glow)',
      },
      backdropBlur: {
        glass: 'var(--blur-glass)',
      },
    },
  },
  plugins: [],
}
