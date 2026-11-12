// The focus route (#/folder/<id>/focus/<clusterId>): Focus and its compare
// mode (FocusContainer.tsx). It stays mounted while the cluster changes (↩,
// ⇧↩): the container opens the new cluster. The contract a route module
// follows is in ../shell/context.tsx.
import { useRoute } from '../shell/context'
import { FocusContainer } from './FocusContainer'

export function FocusRoute() {
  const { folderId, clusterId } = useRoute('focus')
  return <FocusContainer key={folderId} folderId={folderId} clusterId={clusterId} />
}
