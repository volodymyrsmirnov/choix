// Native role items (menus.json `{ "role": …, "label": … }`): what macOS
// draws for them beyond the label, which the manifest carries. Go maps the
// same role names onto Wails' built-in menu roles. Pure data, so the menu
// manifest test can check role shortcuts against item shortcuts.
export const ROLE_ITEMS: Record<string, { display?: string; enabled?: boolean }> = {
  about: {},
  hide: { display: '⌘H' },
  quit: { display: '⌘Q' },
  closeWindow: { display: '⌘W' },
  // Cut / Copy / Paste need a text selection, which a replica never has.
  cut: { display: '⌘X', enabled: false },
  copy: { display: '⌘C', enabled: false },
  paste: { display: '⌘V', enabled: false },
  selectAll: { display: '⌘A' },
  toggleFullscreen: { display: '⌃⌘F' },
  minimize: { display: '⌘M' },
  zoom: {},
}
