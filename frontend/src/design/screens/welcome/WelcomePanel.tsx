import { Banner, BrandMark, Button, ModelRow, Prose } from '../../components'
import { noOrphan } from '../../format'
import type { WelcomeProps } from './props'

function finePrint(models: WelcomeProps['models']): string {
  const clip = models.find((m) => m.kind === 'clip')
  switch (clip?.state) {
    case 'disabled':
      return 'Visual grouping is off, so folders are grouped by time only. Turn it on here or in Settings.'
    case 'failed':
      return 'You can add folders right away. They are grouped by time until the model arrives.'
    case 'ready':
      return 'You can add folders right away.'
    default:
      return 'You can add folders right away. Grouping starts when the download finishes.'
  }
}

/**
 * The Library's first-launch panel (mockup screen 3): what the two models
 * are for, their download rows, and Add Folder, which is never blocked.
 */
export function WelcomePanel({ models, online, onAdd, onRetryModel, onToggleModel }: WelcomeProps) {
  return (
    <div className="h-full overflow-y-auto" data-scroll>
      <div className="min-h-full flex items-center justify-center px-8 py-6">
        <section aria-labelledby="welcome-title" className="w-[520px] max-w-full text-center">
          <div className="flex justify-center mb-3.5">
            <BrandMark size={72} />
          </div>
          <h2 id="welcome-title" className="m-0 mb-1.5 text-22 font-semibold tracking-[-0.02em] text-fg-0">
            {noOrphan('Welcome to Choix')}
          </h2>
          <Prose wrap="pretty" className="mx-auto mb-5 max-w-[500px] text-13 leading-[1.45] text-fg-1">
            Choix downloads two models once, so it can group near-identical shots and rate sharpness. Everything runs on
            this Mac. Your photos never leave it.
          </Prose>
          {!online && (
            <Banner
              tone="warn"
              icon="alert"
              title="This Mac is offline"
              body="The downloads start again when it is back online. You can add folders now."
              className="mb-3 text-left"
            />
          )}
          <div className="mb-[18px] rounded-lg border border-line-1 bg-bg-2 text-left divide-y divide-line-1">
            {models.map((m) => (
              <ModelRow
                key={m.kind}
                id={`welcome-model-${m.kind}`}
                model={m}
                onRetry={() => onRetryModel(m.kind)}
                onToggle={(on) => onToggleModel(m.kind, on)}
              />
            ))}
          </div>
          <Button variant="primary" icon="plus" kbd="⌘O" className="h-8 px-3.5" onClick={onAdd}>
            Add Your First Folder
          </Button>
          <Prose wrap="balance" className="mt-3.5 text-11 leading-[1.5] text-fg-3">
            {finePrint(models)}
          </Prose>
        </section>
      </div>
    </div>
  )
}
