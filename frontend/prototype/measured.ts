// The prototype side of src/design/measured.ts: per `data-measured-by` id,
// the prototype files whose seeded values feed that measured property (a
// Focus or Compare state's zoom and pan). The fingerprint hashes these files
// together with MEASURED[id]'s, so changing a seeded pan changes the state.
// (src/ never names prototype files, so they cannot sit in MEASURED.)
export const PROTO_MEASURED: Record<string, { files: string[] }> = {
  'focus-zoom': { files: ['prototype/scenarios/focus.tsx', 'prototype/scenarios/compare.tsx'] },
}
