// The folder route (#/folder/<id>): the open folder's Folder page (Task 56).
// The contract a route module follows is in ../shell/context.tsx. Keyed by
// the folder id, so another folder gets a fresh container.
import { useRoute } from '../shell/context'
import { FolderContainer } from './FolderContainer'

export function FolderRoute() {
  const { folderId } = useRoute('folder')
  return <FolderContainer key={folderId} folderId={folderId} />
}
