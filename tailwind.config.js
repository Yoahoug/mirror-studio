/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    './internal/web/templates/**/*.html',
    './static_src/**/*.{css,js}',
    './frontend/shared/**/*.{ts,tsx,css}',
    './frontend/console/src/**/*.{ts,tsx}',
    './frontend/workspace/src/**/*.{ts,tsx}',
  ],
  theme: {
    extend: {
      colors: {
        brand: {
          bg: 'var(--color-bg, #f8f9fc)',
          surface: 'var(--color-surface, #ffffff)',
          primary: 'var(--color-ink, #1a1a1a)',
          secondary: 'var(--color-ink-secondary, #4b5563)',
          muted: 'var(--color-muted, #6b7280)',
          submuted: 'var(--color-submuted, #9ca3af)',
          border: 'var(--color-border, #e5e7eb)',
          accent: 'var(--color-accent, #6d5dfc)',
          'accent-hover': 'var(--color-accent-hover, #5b4ce0)',
        },
        semantic: {
          success: 'var(--color-success, #10b981)',
          warning: 'var(--color-warning, #f59e0b)',
          danger: 'var(--color-danger, #ef4444)',
          info: 'var(--color-info, #3b82f6)',
        },
      },
      fontFamily: {
        sans: ['var(--font-sans, -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif)'],
        mono: ['var(--font-mono, ui-monospace, SFMono-Regular, "JetBrains Mono", monospace)'],
      },
      boxShadow: {
        raised: 'var(--shadow-raised, 0 1px 3px 0 rgba(15, 23, 42, 0.04))',
        floating: 'var(--shadow-floating, 0 10px 25px -5px rgba(15, 23, 42, 0.06))',
        overlay: 'var(--shadow-overlay, 0 24px 48px -12px rgba(15, 23, 42, 0.16))',
      },
      borderRadius: {
        xs: 'var(--radius-xs, 6px)',
        sm: 'var(--radius-sm, 8px)',
        md: 'var(--radius-md, 12px)',
        lg: 'var(--radius-lg, 16px)',
        xl: 'var(--radius-xl, 22px)',
        '2xl': 'var(--radius-2xl, 28px)',
        panel: 'var(--radius-xl, 22px)',
      },
    },
  },
  plugins: [],
};
