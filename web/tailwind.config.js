/** @type {import('tailwindcss').Config} */
const v = (name) => `rgb(var(--${name}) / <alpha-value>)`

export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  darkMode: ['class'],
  theme: {
    extend: {
      colors: {
        bg: v('bg'),
        surface: v('surface'),
        sunken: v('sunken'),
        line: v('line'),
        ink: v('ink'),
        muted: v('muted'),
        faint: v('faint'),
        accent: v('accent'),
        'accent-ink': v('accent-ink'),
        ok: v('ok'),
        warn: v('warn'),
        bad: v('bad'),
        info: v('info'),
      },
      fontFamily: {
        display: ['"Bricolage Grotesque Variable"', '"PingFang SC"', '"Microsoft YaHei UI"', 'sans-serif'],
        sans: ['"IBM Plex Sans"', '"PingFang SC"', '"Microsoft YaHei UI"', 'sans-serif'],
        mono: ['"JetBrains Mono"', 'ui-monospace', 'monospace'],
      },
      borderRadius: { xl: '14px', '2xl': '18px' },
      boxShadow: {
        card: '0 1px 0 rgb(var(--line) / 0.6), 0 8px 24px -16px rgb(var(--shadow) / 0.35)',
        pop: '0 20px 48px -20px rgb(var(--shadow) / 0.55)',
      },
      keyframes: {
        rise: { from: { opacity: 0, transform: 'translateY(6px)' }, to: { opacity: 1, transform: 'none' } },
        pulseDot: { '0%,100%': { opacity: 1 }, '50%': { opacity: 0.35 } },
      },
      animation: {
        rise: 'rise .45s cubic-bezier(.2,.7,.2,1) both',
        pulseDot: 'pulseDot 1.8s ease-in-out infinite',
      },
    },
  },
}
