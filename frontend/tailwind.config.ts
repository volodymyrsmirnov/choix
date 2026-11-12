import type { Config } from 'tailwindcss'

// Every value here is a var(--…) reference into src/design/tokens.css.
// fontSize is replaced, so Tailwind's text-xs / text-sm / … do not exist.
// text-12, text-14 and text-30 carry v1's line-heights (text-xs, text-sm,
// text-3xl); the other sizes inherit theirs, as v1's arbitrary sizes did.
// `content` is the app's: its entry and src/ (the app and the design folder),
// test files left out. The prototype build replaces it with its own
// (PROTOTYPE_CONTENT in vite.prototype.config.ts).
const config = {
  content: {
    relative: true,
    files: ['./index.html', './src/**/*.{ts,tsx}', '!**/*.test.ts', '!**/*.test.tsx'],
  },
  theme: {
    fontSize: {
      9: 'var(--text-9)',
      10: 'var(--text-10)',
      11: 'var(--text-11)',
      12: ['var(--text-12)', { lineHeight: 'var(--leading-12)' }],
      13: 'var(--text-13)',
      14: ['var(--text-14)', { lineHeight: 'var(--leading-14)' }],
      15: 'var(--text-15)',
      17: 'var(--text-17)',
      18: 'var(--text-18)',
      20: 'var(--text-20)',
      22: 'var(--text-22)',
      30: ['var(--text-30)', { lineHeight: 'var(--leading-30)' }],
    },
    extend: {
      fontFamily: {
        sans: 'var(--font-sans)',
        mono: 'var(--font-mono)',
      },
      colors: {
        bg: {
          0: 'var(--color-bg-0)',
          1: 'var(--color-bg-1)',
          2: 'var(--color-bg-2)',
          3: 'var(--color-bg-3)',
          4: 'var(--color-bg-4)',
          5: 'var(--color-bg-5)',
        },
        fg: {
          0: 'var(--color-fg-0)',
          1: 'var(--color-fg-1)',
          2: 'var(--color-fg-2)',
          3: 'var(--color-fg-3)',
        },
        line: {
          1: 'var(--color-line-1)',
          2: 'var(--color-line-2)',
        },
        pick: {
          DEFAULT: 'var(--color-pick)',
          hover: 'var(--color-pick-hover)',
          ink: 'var(--color-pick-ink)',
          bg: 'var(--color-pick-bg)',
        },
        reject: {
          DEFAULT: 'var(--color-reject)',
          ink: 'var(--color-reject-ink)',
          bg: 'var(--color-reject-bg)',
          badge: 'var(--color-reject-badge)',
        },
        sel: {
          DEFAULT: 'var(--color-sel)',
          bg: 'var(--color-sel-bg)',
        },
        warn: 'var(--color-warn)',
        scrim: 'var(--color-scrim)',
        backdrop: 'var(--color-backdrop)',
        toast: 'var(--color-toast)',
        panel: 'var(--color-panel)',
        chip: {
          DEFAULT: 'var(--color-chip)',
          line: 'var(--color-chip-line)',
          'line-hover': 'var(--color-chip-line-hover)',
          rule: 'var(--color-chip-rule)',
        },
        kbd: {
          DEFAULT: 'var(--color-kbd)',
          'in-btn': 'var(--color-kbd-in-btn)',
        },
        'spinner-track': 'var(--color-spinner-track)',
        imgph: {
          a: 'var(--color-imgph-a)',
          b: 'var(--color-imgph-b)',
        },
        art: {
          from: 'var(--color-art-from)',
          to: 'var(--color-art-to)',
        },
      },
      borderRadius: {
        sm: 'var(--radius-sm)',
        md: 'var(--radius-md)',
        lg: 'var(--radius-lg)',
        xl: 'var(--radius-xl)',
      },
      boxShadow: {
        pop: 'var(--shadow-pop)',
        'pick-glow': 'var(--shadow-pick-glow)',
        'pick-ring': 'var(--shadow-pick-ring)',
        'sel-ring': 'var(--shadow-sel-ring)',
        'dot-pick': 'var(--shadow-dot-pick)',
        'menu-focus': 'var(--shadow-menu-focus)',
        art: 'var(--shadow-art)',
      },
      letterSpacing: {
        tightish: 'var(--tracking-tightish)',
      },
      transitionDuration: {
        fast: 'var(--dur-fast)',
        base: 'var(--dur-base)',
        slow: 'var(--dur-slow)',
      },
    },
  },
  plugins: [],
} satisfies Config

export default config
