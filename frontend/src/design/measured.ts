// Values computed from layout at run time (an element marked
// `data-measured="width left" data-measured-by="<id>"`) are left out of a
// state's fingerprint. In their place the fingerprint carries a hash of the
// files that compute them, so changing that code still changes the state.
//
// Each `data-measured-by` id used anywhere in src/design/ is listed here with
// the files (relative to frontend/) that compute it. The shared components
// measure nothing; screen groups add their entries (for example the Folder
// page's `+N` strip overflow) together with the component that measures.
// Prototype files that seed a measured value (a state's zoom and pan) are
// listed on the prototype side, in its own measured.ts (src never names the
// prototype's files); the fingerprint hashes the files of both lists.
//
// `props` lists the computed properties an element measured by the id may
// leave out of the fingerprint (tools/extract.ts throws on any other). It
// may be empty: a scroller whose position code sets (scrollTop / scrollTo)
// drops nothing, but its id brings the files that compute the scroll into
// the hash, since a scroll position is visible and not in the tree.
export const MEASURED: Record<string, { files: string[]; props: string[] }> = {
  // The Folder page's thumbnail strip: its edge-fade mask and its "+N" pill.
  'cluster-strip': { files: ['src/design/components/ClusterStrip.tsx'], props: ['mask-image', '-webkit-mask-image'] },
  // Focus / Compare pixel-peep: the photo's translate + scale. Pinch and
  // ⌘-wheel give any zoom from 1 to 8 and a drag any pan.
  'focus-zoom': {
    files: [
      'src/design/components/FocusMedia.tsx',
      'src/design/screens/focus/logic.ts',
      'src/design/screens/focus/props.ts',
    ],
    props: ['transform'],
  },
  // Focus's rail scrolls itself to the active photo.
  'focus-rail-scroll': { files: ['src/design/components/FocusRail.tsx'], props: [] },
  // The Folder page's cluster list scrolls the active cluster into view.
  'cluster-list-scroll': { files: ['src/design/components/ClusterList.tsx', 'src/design/screens/folder/FolderScreen.tsx'], props: [] },
  // Settings opens scrolled to its section or to the revealed field.
  'settings-scroll': { files: ['src/design/screens/settings/SettingsScreen.tsx'], props: [] },
}
