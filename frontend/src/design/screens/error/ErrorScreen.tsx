import { BrandMark, Button, EmptyState, TitleBar } from '../../components'
import { ERROR_COPY, type ErrorScreenProps } from './props'

/**
 * The full-window error screen (props.ts): the title bar with the brand,
 * then, centred, what happened, the error's message (selectable, at most
 * four lines; Copy Error Details copies all of it) and the two actions,
 * Reload last as the primary one.
 */
export function ErrorScreen(p: ErrorScreenProps) {
  return (
    <div className="relative flex flex-col h-full min-h-0">
      <TitleBar leading={<BrandMark />} title="Choix" />
      <main className="flex-1 min-h-0 overflow-y-auto" data-scroll>
        <div className="min-h-full flex items-center justify-center">
          <EmptyState art={false} level={2} title={ERROR_COPY.title} body={ERROR_COPY.body}>
            <div data-testid="app-error" className="flex flex-col items-center gap-5">
              <p
                data-selectable="true"
                className="m-0 w-full px-3 py-2 rounded-md border border-line-1 bg-bg-2 text-left font-mono text-11 leading-[1.5] text-fg-1 break-words line-clamp-4"
              >
                {p.details}
              </p>
              <div className="flex gap-2">
                <Button variant="default" onClick={p.onCopyDetails}>
                  Copy Error Details
                </Button>
                <Button variant="primary" icon="refresh" onClick={p.onReload}>
                  Reload
                </Button>
              </div>
            </div>
          </EmptyState>
        </div>
      </main>
    </div>
  )
}
