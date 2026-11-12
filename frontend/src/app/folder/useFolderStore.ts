// The open folder's FolderStore on the real bindings, shared by the Folder
// and Focus routes (one per shell, see createFolderStores): Focus picks
// through the same store, so the grid shows them on the way back. Each route
// starts it while mounted (`useEffect(() => store.start(), [store])`);
// starting again loads the grid again.
import { Catalog } from '../shell/api'
import type { Shell } from '../shell/context'
import { useShell } from '../shell/context'
import { on } from '../shell/events'
import { createFolderStore, createFolderStores, type FolderStore, type FolderStores } from './folderStore'

const registries = new WeakMap<Shell, FolderStores>()

function storesOf(shell: Shell): FolderStores {
  let stores = registries.get(shell)
  if (!stores) {
    stores = createFolderStores(shell.session, (folderId) =>
      createFolderStore({
        folderId,
        session: shell.session,
        on,
        clusters: (id) => Catalog.Clusters(id),
        ratings: (id) => Catalog.Ratings(id),
        setPick: (id, fileId, action) => Catalog.SetPick(id, fileId, action),
        undo: (id) => Catalog.Undo(id),
        recluster: (id) => Catalog.Recluster(id),
        hideRejected: () => shell.settings.get().value?.hide_rejected_photos ?? false,
      }),
    )
    registries.set(shell, stores)
  }
  return stores
}

/** The store of the open folder `folderId` (the same object while it stays open). */
export function useFolderStore(folderId: string): FolderStore {
  return storesOf(useShell()).get(folderId)
}
