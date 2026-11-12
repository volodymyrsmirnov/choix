// The library route (#/): the Library, with its welcome panel (Task 54).
// The container and its store sit beside this file; the store itself is
// the shell's (shell.library), so it runs on every route. The contract a
// route module follows is in ../shell/context.tsx.
import { LibraryContainer } from './LibraryContainer'

export function LibraryRoute() {
  return <LibraryContainer />
}
