import { noOrphan } from '../format'
import { Button } from './Button'
import { Icon } from './Icon'
import { Prose } from './Prose'

/**
 * Where folders are added: the empty Library's dashed panel (mockup screen
 * 4), or, with `overlay`, a drop target laid over a filled Library while
 * folders are dragged onto the window. `active` is the look while a drag is
 * over the window.
 */
export function DropZone({ active, onAdd, overlay }: { active: boolean; onAdd: () => void; overlay?: boolean }) {
  if (overlay) {
    return (
      // data-overlay: while a drag is over the window it lies over the
      // timeline on purpose (an overlay for the occlusion check; it is not a
      // registered overlay: Escape and the flows do not close it, the drag
      // leaving the window does).
      <div
        data-overlay="drop-zone"
        data-drop-zone="overlay"
        className="absolute inset-4 z-30 rounded-xl border-2 border-dashed border-pick bg-backdrop flex flex-col items-center justify-center gap-3 text-center pointer-events-none"
      >
        <Icon name="plus" size={32} className="text-pick" />
        <div className="text-17 font-semibold text-fg-0">{noOrphan('Drop to add to the Library')}</div>
        <Prose className="text-13 text-fg-1 max-w-[360px]">Choix adds each folder and starts scanning it.</Prose>
      </div>
    )
  }
  return (
    <div
      data-drop-zone="panel"
      data-active={active ? 'true' : undefined}
      className={`w-[560px] max-w-full rounded-xl border-[1.5px] border-dashed px-[30px] py-11 text-center transition-colors ${active ? 'border-pick bg-pick-bg' : 'border-line-2 bg-bg-1'}`}
    >
      <div className={`flex justify-center ${active ? 'text-pick' : 'text-fg-2'}`}>
        <Icon name={active ? 'plus' : 'folder'} size={40} />
      </div>
      <h2 className="m-0 mt-2.5 mb-1.5 text-20 font-semibold tracking-[-0.02em] text-fg-0">
        {noOrphan(active ? 'Drop to add the folder' : 'Add a folder of photos')}
      </h2>
      <Prose wrap="balance" className="text-13 text-fg-1 leading-[1.45] mx-auto mb-[18px] max-w-[400px]">
        {active
          ? 'Choix adds it to the Library and starts scanning. Originals are never changed.'
          : 'Choix groups the shots by camera, time and look, so you only compare the ones that matter.'}
      </Prose>
      <Button variant="primary" icon="plus" kbd="⌘O" className="h-8 px-3.5" onClick={onAdd} disabled={active}>
        Add Folder…
      </Button>
      <p className="m-0 mt-3.5 text-11 text-fg-3 leading-[1.5]" data-wrap="balance">
        or drag a folder onto this window
        <br />
        {/* noOrphan by hand: the last three words are bound around the mono span. */}
        {'Originals are never changed. Picks are copied to a '}
        <span className="font-mono">picks</span>
        {' folder inside.'}
      </p>
    </div>
  )
}
