// The parity build's System binding (the generated system/service.ts's
// functions, same names and types; control.ts says how they answer).
import type { CancellablePromise } from '@wailsio/runtime/cancellable'
import type * as $models from '../../../../bindings/github.com/volodymyrsmirnov/choix/internal/app/system/models'
import { done } from '../control'

/**
 * A fixed build: sandboxed, single keys owned by the webview (System.Info
 * today), no window measured (windowButtonsRight and windowButtonsCenterY 0:
 * the title bar keeps the design's inset and vertical alignment, as in the
 * prototype) and not in full screen.
 */
export function Info(): CancellablePromise<$models.Info> {
  return done<$models.Info>({
    name: 'Choix',
    version: '0.0.0-parity',
    commit: 'parity',
    sandboxed: true,
    singleKeysNative: false,
    windowButtonsRight: 0,
    windowButtonsCenterY: 0,
    windowFullScreen: false,
  })
}

export function OpenHelp(): CancellablePromise<void> {
  return done()
}

export function OpenPicksFolder(_folderID: string): CancellablePromise<void> {
  return done()
}

/** Error reports go nowhere in the parity build. */
export function ReportClientError(_where: string, _message: string, _stack: string): CancellablePromise<void> {
  return done()
}

export function RevealFolder(_folderID: string): CancellablePromise<void> {
  return done()
}

export function SetMenuContext(_c: $models.MenuContext): CancellablePromise<void> {
  return done()
}

export function ShowFolderMenu(_folderID: string, _x: number, _y: number): CancellablePromise<void> {
  return done()
}

export function TitleBarDoubleClick(): CancellablePromise<void> {
  return done()
}
