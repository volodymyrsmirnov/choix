// Where the Info panel's open state lives between visits (v1's key and
// values): localStorage["choix.focus.showInfo"], "1" open, "0" closed. Open
// until the user closes it, as in v1. A storage that is missing or throws
// (a locked-down webview) is ignored: the panel is then simply open.
import type { FocusPrefs } from './focusStore'

export const SHOW_INFO_KEY = 'choix.focus.showInfo'

type KeyValue = { getItem(key: string): string | null; setItem(key: string, value: string): void }

export function showInfoPrefs(storage: () => KeyValue | undefined): FocusPrefs {
  return {
    showInfo() {
      try {
        const v = storage()?.getItem(SHOW_INFO_KEY)
        return v === null || v === undefined ? true : v === '1'
      } catch {
        return true
      }
    },
    setShowInfo(open) {
      try {
        storage()?.setItem(SHOW_INFO_KEY, open ? '1' : '0')
      } catch {
        // Not kept; the panel still follows the toggle for this visit.
      }
    },
  }
}
