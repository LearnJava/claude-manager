/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{svelte,ts,js}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        // Tokens are sourced from CSS variables defined in style.css so we
        // can switch palettes by toggling the `dark` class on <html>.
        bg: {
          DEFAULT:  'rgb(var(--c-bg) / <alpha-value>)',
          panel:    'rgb(var(--c-bg-panel) / <alpha-value>)',
          elevated: 'rgb(var(--c-bg-elevated) / <alpha-value>)',
          border:   'rgb(var(--c-bg-border) / <alpha-value>)',
        },
        text: {
          DEFAULT: 'rgb(var(--c-text) / <alpha-value>)',
          muted:   'rgb(var(--c-text-muted) / <alpha-value>)',
          dim:     'rgb(var(--c-text-dim) / <alpha-value>)',
        },
        status: {
          working:   '#22c55e',
          waiting:   '#f97316',
          ratelimit: '#eab308',
          error:     '#ef4444',
          idle:      '#6b7280',
          starting:  '#3b82f6',
        },
      },
    },
  },
  plugins: [],
}
