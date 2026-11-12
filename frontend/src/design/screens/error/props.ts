/**
 * The app's last resort (spec §4.6): a screen failed to render, and returning
 * to the Library (the app's first answer to a render error) failed too. The
 * whole window shows what happened, that nothing was lost, and a way out.
 */
export interface ErrorScreenProps {
  /** The error's message, shown as it is and selectable. */
  details: string
  /** Starts the window again from the Library. */
  onReload: () => void
  /** Copies the error's details (the app decides what they hold: the message and its stack). */
  onCopyDetails: () => void
}

/** The screen's copy. Picks are safe because each is saved the moment it is made (hard rule 2). */
export const ERROR_COPY = {
  title: 'Choix could not show this screen',
  body: 'Your picks are safe: each one is saved the moment you make it. Reload to start again from the Library.',
} as const
