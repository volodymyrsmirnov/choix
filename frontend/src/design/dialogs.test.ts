import { expect, test } from 'bun:test'
import { dialog, DIALOGS } from './dialogs'

test('remove-folder has a default and a cancel button', () => {
  const d = DIALOGS['remove-folder']
  expect(d).toBeDefined()
  expect(d!.buttons.map((b) => b.role)).toEqual(['default', 'cancel'])
  expect(d!.buttons.map((b) => b.label)).toEqual(['Remove', 'Cancel'])
})

test('dialog() substitutes {name} and leaves the manifest untouched', () => {
  const d = dialog('remove-folder', { name: 'Lisbon' })
  expect(d.title).toBe('Remove “Lisbon” from Choix?')
  expect(DIALOGS['remove-folder']!.title).toContain('{name}')
})

test('dialog() throws for an unknown id', () => {
  expect(() => dialog('nope')).toThrow(/nope/)
})
