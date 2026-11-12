// The app's last resort: the Library itself failed to render (RouteBoundary
// has no route above it to return to), so the design's ErrorScreen (#40)
// fills the window. It shows the error's message; Copy Error Details puts
// the message, the error's stack and React's component stack on the
// clipboard; Reload loads the window again, from the Library.
//
// The parity build shows the same screen from the fixture's
// `ui['shell.renderError']` (App.tsx), as nothing else can make a screen
// fail there.
import { ErrorScreen } from '../../design/screens/error/ErrorScreen'
import { errorParts } from './clientErrors'

/** What Copy Error Details copies: the message, the error's stack, then the component stack. */
export function renderErrorDetails(error: unknown, componentStack: string): string {
  const { message, stack } = errorParts(error)
  const parts = [message]
  if (stack.trim() !== '') parts.push(stack)
  if (componentStack.trim() !== '') parts.push(`Components:${componentStack.startsWith('\n') ? '' : '\n'}${componentStack}`)
  return parts.join('\n\n')
}

interface ClipboardHost {
  clipboard?: { writeText(text: string): Promise<void> }
}

interface CopyField {
  value: string
  style: { opacity?: string }
  setAttribute(name: string, value: string): void
  select(): void
  remove(): void
}

interface CopyDocument {
  createElement(tag: 'textarea'): CopyField
  body: { appendChild(node: never): unknown }
  execCommand(command: 'copy'): boolean
}

/**
 * Puts `text` on the clipboard: the clipboard API, else (refused, or not
 * there: a webview may offer neither) a selected, read-only text field and
 * the copy command.
 */
export async function copyText(text: string, nav: ClipboardHost, doc: CopyDocument | null): Promise<void> {
  try {
    if (nav.clipboard) {
      await nav.clipboard.writeText(text)
      return
    }
  } catch {
    // Falls through to the copy command.
  }
  if (!doc) return
  const field = doc.createElement('textarea')
  field.value = text
  field.setAttribute('readonly', '')
  field.style.opacity = '0'
  doc.body.appendChild(field as never)
  field.select()
  try {
    doc.execCommand('copy')
  } finally {
    field.remove()
  }
}

/** Loads the page again without its route, so the window starts from the Library. */
export function reloadToLibrary(location: { pathname: string; search: string; replace(url: string): void }): void {
  location.replace(`${location.pathname}${location.search}`)
}

/** ErrorScreen with the app's actions. */
export function AppErrorScreen(p: { details: string; full: string; copy(text: string): void; reload(): void }) {
  return <ErrorScreen details={p.details} onCopyDetails={() => p.copy(p.full)} onReload={p.reload} />
}
