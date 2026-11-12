// The error screen's parity constants (cases.ts, fixtures/welcome.ts). A
// module of its own with no imports: cases.ts runs on Node and cannot import
// the scenarios, and the fixtures must not import cases.ts (it imports them).

/** The error screen's state. */
export const ERROR_SCREEN_STATE = 40
/** The parity channel's key for the error the screen shows (src/app/shell/parityUi.ts uiOverride). */
export const RENDER_ERROR_KEY = 'shell.renderError'
