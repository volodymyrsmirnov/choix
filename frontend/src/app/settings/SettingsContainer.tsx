// Settings: the design's SettingsScreen, fed from the shell. The page logic
// is settingsDraft.ts; this file binds it to the bindings (Settings.Save,
// Models.Retry), the shell's stores (settings, models from
// `models:progress`, quality from `quality:status`), the router and the
// toasts. Menu and keyboard actions need nothing here: Back (⌘[) is the
// shell's `back`, which the leave guard holds while edits are unsaved, and
// the help button runs the shell's `shortcuts`.
import { useEffect, useState, useSyncExternalStore } from 'react'
import type { SettingsProps } from '../../design/screens/settings/props'
import { SettingsScreen } from '../../design/screens/settings/SettingsScreen'
import { Models, Settings } from '../shell/api'
import { useShell, useToastProps } from '../shell/context'
import { uiOverride } from '../shell/parityUi'
import { useStore } from '../shell/store'
import { backLabel, createSettingsDraft } from './settingsDraft'

/**
 * Screen-local UI state that real data has no source for yet: `reveal`, the
 * opening scroll to the quality field that the approved quality states
 * (505, 506, 509, 513–515) show. Nothing in the app opens Settings that way
 * (no route parameter, nothing clickable), so the app passes the neutral
 * value; only the parity build hands it a value, the fixture's
 * `settings.reveal` (shell/parityUi.ts uiOverride, undefined in every other
 * build). Read once, when the page opens: the screen uses it for its first
 * scroll only, and the controller makes the field's section the sidebar's
 * (ctl.reveal).
 */
function openingScroll(): Pick<SettingsProps, 'reveal'> {
  const reveal = uiOverride<SettingsProps['reveal']>('settings.reveal')
  return reveal === undefined ? {} : { reveal }
}

export function SettingsContainer() {
  const shell = useShell()
  const toastProps = useToastProps()
  const [screenLocal] = useState(openingScroll)
  const [ctl] = useState(() => {
    const c = createSettingsDraft({
      save: (patch) => Settings.Save(patch),
      retry: (kind) => Models.Retry(kind),
      settings: shell.settings,
      models: shell.models,
      quality: shell.quality,
      router: shell.router,
      toast: (t) => {
        shell.toasts.push(t)
      },
    })
    if (screenLocal.reveal !== undefined) c.reveal(screenLocal.reveal)
    return c
  })
  useEffect(() => ctl.start(), [ctl])

  const st = useSyncExternalStore(ctl.subscribe, ctl.state)
  const models = useStore(shell.models).value
  const quality = useStore(shell.quality).value
  const from = useSyncExternalStore(shell.router.subscribe, shell.router.settingsFrom)
  const session = useSyncExternalStore(shell.session.subscribe, shell.session.get)

  return (
    <SettingsScreen
      {...screenLocal}
      saved={st.saved}
      loadError={st.loadError}
      draft={st.draft}
      errors={st.errors}
      activeSection={st.activeSection}
      saving={st.saving}
      quality={quality ?? null}
      retrying={st.retrying}
      models={models ?? []}
      leavePrompt={st.leavePrompt}
      toasts={toastProps.toasts}
      onUndoToast={toastProps.onUndoToast}
      onDismissToast={toastProps.onDismissToast}
      onChange={ctl.change}
      onSection={ctl.section}
      onSave={() => void ctl.save()}
      onRevert={ctl.revert}
      backTo={backLabel(from, session)}
      onBack={ctl.back}
      onHelp={() => shell.actions.runAction('shortcuts')}
      onReload={() => void ctl.reload()}
      onRetryQuality={() => void ctl.retry('quality')}
      onRetryModel={(kind) => void ctl.retry(kind)}
      onConfirmLeave={ctl.confirmLeave}
    />
  )
}
