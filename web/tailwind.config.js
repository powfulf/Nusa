/**
 * Tailwind reads every visual value through the custom properties in
 * src/styles/tokens.css, which are themselves a transcription of DESIGN.md.
 * Nothing here decides anything.
 *
 * `colors` REPLACES Tailwind's palette rather than extending it, so utilities
 * like `bg-red-500` or `text-gray-400` do not exist. Tailwind fails silently
 * on an unknown colour class — it simply emits no rule — so `npm run
 * lint:tokens` also greps for the raw values that would have produced one.
 *
 * `screens` is the single unavoidable exception to "raw values live only in
 * tokens.css": a media query cannot read a custom property. The breakpoints
 * below are transcribed from DESIGN.md § Responsiveness.
 *
 * @type {import('tailwindcss').Config}
 */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    // DESIGN.md § Responsiveness. The base band is verified down to 360px,
    // which is narrower than the smallest breakpoint on purpose.
    screens: {
      sm: '480px',
      md: '768px',
      lg: '1024px',
      xl: '1280px',
      '2xl': '1536px',
    },

    colors: {
      transparent: 'transparent',
      current: 'currentColor',
      inherit: 'inherit',

      surface: {
        base: 'var(--surface-base)',
        DEFAULT: 'var(--surface-default)',
        sunken: 'var(--surface-sunken)',
        inverse: 'var(--surface-inverse)',
      },
      text: {
        primary: 'var(--text-primary)',
        secondary: 'var(--text-secondary)',
        muted: 'var(--text-muted)',
        'on-inverse': 'var(--text-on-inverse)',
      },
      border: {
        subtle: 'var(--border-subtle)',
        strong: 'var(--border-strong)',
        divider: 'var(--divider)',
      },

      // Two values per status. `fill` sits behind content; `content` is
      // mandatory wherever the colour has to be read.
      brand: {
        fill: 'var(--status-brand-fill)',
        content: 'var(--status-brand-content)',
        surface: 'var(--status-brand-surface)',
      },
      success: {
        fill: 'var(--status-success-fill)',
        content: 'var(--status-success-content)',
        surface: 'var(--status-success-surface)',
      },
      warning: {
        fill: 'var(--status-warning-fill)',
        content: 'var(--status-warning-content)',
        surface: 'var(--status-warning-surface)',
      },
      error: {
        fill: 'var(--status-error-fill)',
        content: 'var(--status-error-content)',
        surface: 'var(--status-error-surface)',
      },
      info: {
        fill: 'var(--status-info-fill)',
        content: 'var(--status-info-content)',
        surface: 'var(--status-info-surface)',
      },

      control: {
        primary: 'var(--control-primary-fill)',
        'primary-hover': 'var(--control-primary-hover)',
        'primary-text': 'var(--control-primary-text)',
        'secondary-hover': 'var(--control-secondary-hover)',
        destructive: 'var(--control-destructive-fill)',
        'destructive-hover': 'var(--control-destructive-hover)',
        selected: 'var(--control-selected)',
      },

      focus: {
        DEFAULT: 'var(--focus-ring)',
        inverse: 'var(--focus-ring-inverse)',
      },
    },

    extend: {
      fontFamily: {
        heading: 'var(--font-heading)',
        body: 'var(--font-body)',
        mono: 'var(--font-mono)',
      },

      // Each step pairs its size with the line height DESIGN.md gives it, so a
      // heading cannot pick up the wrong leading by accident.
      fontSize: {
        display: ['var(--text-display)', { lineHeight: 'var(--leading-display)' }],
        h1: ['var(--text-h1)', { lineHeight: 'var(--leading-h1)' }],
        h2: ['var(--text-h2)', { lineHeight: 'var(--leading-h2)' }],
        h3: ['var(--text-h3)', { lineHeight: 'var(--leading-h3)' }],
        h4: ['var(--text-h4)', { lineHeight: 'var(--leading-h4)' }],
        'body-lg': ['var(--text-body-lg)', { lineHeight: 'var(--leading-body)' }],
        body: ['var(--text-body)', { lineHeight: 'var(--leading-body)' }],
        'body-sm': ['var(--text-body-sm)', { lineHeight: 'var(--leading-body-sm)' }],
        caption: ['var(--text-caption)', { lineHeight: 'var(--leading-caption)' }],
        code: ['var(--text-code)', { lineHeight: 'var(--leading-body)' }],
      },

      spacing: {
        xs: 'var(--space-xs)',
        sm: 'var(--space-sm)',
        md: 'var(--space-md)',
        lg: 'var(--space-lg)',
        xl: 'var(--space-xl)',
        '2xl': 'var(--space-2xl)',
        '3xl': 'var(--space-3xl)',
        row: 'var(--row-height)',
        touch: 'var(--touch-target)',
      },

      borderRadius: {
        sm: 'var(--radius-sm)',
        DEFAULT: 'var(--radius-default)',
        md: 'var(--radius-md)',
        lg: 'var(--radius-lg)',
        full: 'var(--radius-full)',
      },

      boxShadow: {
        sm: 'var(--elevation-sm)',
        DEFAULT: 'var(--elevation-default)',
        md: 'var(--elevation-md)',
        lg: 'var(--elevation-lg)',
        none: 'none',
      },

      maxWidth: {
        prose: 'var(--measure-prose)',
        shell: 'var(--measure-shell)',
      },

      transitionDuration: {
        instant: 'var(--motion-instant)',
        fast: 'var(--motion-fast)',
        base: 'var(--motion-base)',
        slow: 'var(--motion-slow)',
      },

      transitionTimingFunction: {
        standard: 'var(--ease-standard)',
        enter: 'var(--ease-enter)',
        exit: 'var(--ease-exit)',
      },
    },
  },
  plugins: [],
}
