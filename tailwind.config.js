module.exports = {
  content: [
    './templates/**/*.html',
    './apps/**/*.py',
    './frontend/console/src/**/*.{ts,tsx}',
    './frontend/workspace/src/**/*.{ts,tsx}',
  ],
  theme: {
    extend: {
      colors: {
        brand: {
          primary: '#1a1a1a',
          muted: '#6b7280',
          border: '#e5e7eb',
          bg: '#f9fafb',
          50: '#eef4ff',
          100: '#d9e6ff',
          500: '#2f6bff',
          600: '#2557d8',
          700: '#1d45aa'
        },
        accent: '#14b8a6',
        warning: '#f97316'
      },
      fontFamily: {
        sans: ['Inter', 'ui-sans-serif', 'system-ui', 'sans-serif'],
        mono: ['JetBrains Mono', 'ui-monospace', 'SFMono-Regular', 'monospace']
      },
      boxShadow: {
        soft: '0 20px 60px rgba(15, 23, 42, 0.08)'
      },
      borderRadius: {
        panel: '18px'
      }
    }
  },
  plugins: []
};
