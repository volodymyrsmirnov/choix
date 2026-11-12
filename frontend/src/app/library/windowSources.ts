// What the Library reads from the window itself, as sources for
// useSyncExternalStore (subscribe + get), each taking the window or
// document it reads so the tests can hand it a fake.
//
//   dropSource    folders dragged over the window. Wails' runtime marks
//                 the drop target (#root, data-file-drop-target in
//                 index.html) with its `file-drop-target-active` class
//                 while files are over it, from the page's drag events
//                 and from the native drag on macOS alike; this follows
//                 that class. The drop itself is Go's (window drop →
//                 Library.AddFolders): nothing here handles it.
//   keepScroll    the Library's scroll offset across visits (the router
//                 keeps offsets per route).

export interface Source<T> {
  subscribe(onChange: () => void): () => void
  get(): T
}

const DROP_TARGET = '[data-file-drop-target]'
const DROP_ACTIVE = 'file-drop-target-active'

interface DropTarget {
  classList: { contains(name: string): boolean }
}

interface ClassObserver {
  observe(el: never, opts: { attributes: boolean; attributeFilter: string[] }): void
  disconnect(): void
}

export function dropSource(
  doc: { querySelector(selector: string): DropTarget | null },
  Observer: new (onChange: () => void) => ClassObserver,
): Source<boolean> {
  return {
    subscribe(onChange) {
      const el = doc.querySelector(DROP_TARGET)
      if (!el) return () => {}
      const observer = new Observer(onChange)
      observer.observe(el as never, { attributes: true, attributeFilter: ['class'] })
      return () => observer.disconnect()
    },
    get: () => doc.querySelector(DROP_TARGET)?.classList.contains(DROP_ACTIVE) ?? false,
  }
}

export interface Scrollable {
  scrollTop: number
  addEventListener(type: 'scroll', fn: () => void, opts?: { passive: boolean }): void
  removeEventListener(type: 'scroll', fn: () => void): void
}

/** Puts the saved offset back on `el` and saves it on every scroll; returns stop. */
export function keepScroll(el: Scrollable, memory: { offset(): number; save(top: number): void }): () => void {
  el.scrollTop = memory.offset()
  const onScroll = () => memory.save(el.scrollTop)
  el.addEventListener('scroll', onScroll, { passive: true })
  return () => el.removeEventListener('scroll', onScroll)
}
