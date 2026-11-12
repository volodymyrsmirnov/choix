// PostCSS pipeline for every frontend build: Tailwind first, then the
// data-force twins for interaction selectors (tools/postcss-force.cjs).
const path = require('node:path')

module.exports = {
  plugins: [
    require('tailwindcss')({ config: path.join(__dirname, 'tailwind.config.ts') }),
    require('./tools/postcss-force.cjs')(),
  ],
}
