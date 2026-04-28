/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ['./src/**/*.{ts,tsx,html}', './index.html'],
  darkMode: 'class',
  theme: {
    extend: {
      fontFamily: {
        sans: ['Inter', 'ui-sans-serif', 'system-ui', 'sans-serif'],
        mono: ['"JetBrains Mono"', 'ui-monospace', 'SFMono-Regular', 'Menlo', 'monospace'],
      },
      colors: {
        bg: {
          0: 'oklch(0.16 0.004 240)',
          1: 'oklch(0.19 0.004 240)',
          2: 'oklch(0.22 0.005 240)',
          3: 'oklch(0.26 0.006 240)',
          4: 'oklch(0.31 0.007 240)',
          5: 'oklch(0.38 0.008 240)',
        },
        fg: {
          0: 'oklch(0.97 0.003 240)',
          1: 'oklch(0.82 0.005 240)',
          2: 'oklch(0.62 0.006 240)',
          3: 'oklch(0.48 0.006 240)',
        },
        line: {
          1: 'oklch(0.28 0.005 240 / 0.9)',
          2: 'oklch(0.34 0.006 240 / 0.9)',
        },
        pick: {
          DEFAULT: 'oklch(0.72 0.18 145)',
          hover: 'oklch(0.78 0.18 145)',
          ink: 'oklch(0.18 0.04 145)',
          bg: 'oklch(0.72 0.18 145 / 0.14)',
        },
        reject: {
          DEFAULT: 'oklch(0.66 0.20 25)',
          ink: 'oklch(0.16 0.06 25)',
          bg: 'oklch(0.66 0.20 25 / 0.14)',
        },
        sel: {
          DEFAULT: 'oklch(0.72 0.13 235)',
          bg: 'oklch(0.72 0.13 235 / 0.18)',
        },
      },
      borderRadius: { sm: '4px', md: '6px', lg: '10px' },
      boxShadow: {
        'pop': '0 8px 24px oklch(0 0 0 / 0.5), inset 0 1px 0 0 oklch(1 0 0 / 0.05)',
        'pick-glow': '0 0 0 3px oklch(0.72 0.18 145 / 0.14), 0 4px 16px oklch(0.72 0.18 145 / 0.18)',
      },
      letterSpacing: { tightish: '-0.005em' },
    },
  },
  plugins: [],
}
