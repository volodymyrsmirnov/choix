// What Go's Models shim (internal/app/models, until phase B) answers to
// Status: clip, then quality, both ready, sizes 0. Nothing is downloaded any
// more. Its own module so service.ts exports exactly the generated names;
// phase B deletes it with the binding.

/** The shim's Status answer, as plain data. */
export function shimStatus() {
  return [
    { kind: 'clip' as const, state: 'ready' as const, received: 0, size: 0 },
    { kind: 'quality' as const, state: 'ready' as const, received: 0, size: 0 },
  ]
}
