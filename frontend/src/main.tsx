// The app's entry: the design tokens, then the design's base styles (that
// order), then the App in #root. No StrictMode (see App).
import { createRoot } from 'react-dom/client'
import './design/tokens.css'
import './design/base.css'
import { App } from './app/shell/App'

const root = document.getElementById('root')
if (!root) throw new Error('index.html has no #root')
createRoot(root).render(<App />)
