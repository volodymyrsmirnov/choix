import { expect, test } from 'bun:test'
import { scenarios as gallery } from '../scenarios/gallery'
import { scenarios as library } from '../scenarios/library'
import { scenarios as welcome } from '../scenarios/welcome'
import { search } from './search'

const STATES = [...welcome, ...library, ...gallery].map((s) => ({ n: s.n, group: s.group, label: s.label }))

test("search('empty') includes s100", () => {
  expect(search('empty', STATES).map((s) => s.n)).toContain(100)
})

test("search('s102') and search('102') return s102", () => {
  expect(search('s102', STATES).map((s) => s.n)).toEqual([102])
  expect(search('102', STATES).map((s) => s.n)).toEqual([102])
})

test('a number prefix lists every state it starts', () => {
  const ns = search('10', STATES).map((s) => s.n)
  expect(ns).toContain(100)
  expect(ns).toContain(109)
  expect(ns).not.toContain(11)
})

test('every word must match, in any order, ignoring case', () => {
  const ns = search('ROW offline', STATES).map((s) => s.n)
  expect(ns).toEqual([107])
})

test('a group name matches its states', () => {
  expect(search('gallery', STATES).map((s) => s.n)).toEqual([800, 801, 802, 803, 804, 805, 806, 807, 808, 809])
})

test('an empty query returns everything, in order', () => {
  const all = search('  ', STATES).map((s) => s.n)
  expect(all).toEqual([...all].sort((a, b) => a - b))
  expect(all.length).toBe(STATES.length)
})
