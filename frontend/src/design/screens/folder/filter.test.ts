import { expect, test } from 'bun:test'
import type { Cluster, FolderFilter, Member } from '../../types'
import { filterClusters } from './filter'

const m = (file_id: number, path: string, extra: Partial<Member> = {}): Member => ({
  file_id,
  path,
  content_hash: `h${file_id}`,
  picked: false,
  rejected: false,
  kind: 'photo',
  format: 'RAF',
  ...extra,
})

const A: Cluster = { id: 1, device: 'X-T5', time: 'Oct 14 · 07:50', members: [m(1, 'DCIM/DSCF0101.RAF'), m(2, 'DCIM/DSCF0102.RAF')] }
const B: Cluster = { id: 2, device: 'iPhone 15 Pro', time: 'Oct 14 · 09:12', members: [m(3, 'Phone/IMG_2231.HEIC')] }
const C: Cluster = { id: 3, device: 'X-T5 + iPhone 15 Pro', time: 'Oct 15 · 18:40', members: [m(4, 'DCIM/DSCF0230.RAF'), m(5, 'Phone/IMG_2290.HEIC')] }
const D: Cluster = { id: 4, device: 'X-T5', time: 'Oct 16 · 06:05', members: [m(6, 'DCIM/DSCF0301.RAF')] }
const ALL = [A, B, C, D]

const f = (patch: Partial<FolderFilter>): FolderFilter => ({ query: '', device: '', sort: 'time-asc', ...patch })
const ids = (cs: Cluster[]) => cs.map((c) => c.id)

test('no filter keeps every cluster in the order given', () => {
  expect(ids(filterClusters(ALL, f({})))).toEqual([1, 2, 3, 4])
})

test('latest first reverses the order, as v1 did, without touching the input', () => {
  expect(ids(filterClusters(ALL, f({ sort: 'time-desc' })))).toEqual([4, 3, 2, 1])
  expect(ids(ALL)).toEqual([1, 2, 3, 4])
})

test('the camera filter keeps that camera’s clusters and the merged clusters it is part of', () => {
  expect(ids(filterClusters(ALL, f({ device: 'X-T5' })))).toEqual([1, 3, 4])
  expect(ids(filterClusters(ALL, f({ device: 'iPhone 15 Pro' })))).toEqual([2, 3])
  // A merged label chosen as such (v1 listed it) still matches exactly.
  expect(ids(filterClusters(ALL, f({ device: 'X-T5 + iPhone 15 Pro' })))).toEqual([3])
  expect(ids(filterClusters(ALL, f({ device: 'GR IIIx' })))).toEqual([])
})

test('the query matches the camera, ignoring case and surrounding spaces', () => {
  expect(ids(filterClusters(ALL, f({ query: '  iphone ' })))).toEqual([2, 3])
})

test('the query matches the cluster time', () => {
  expect(ids(filterClusters(ALL, f({ query: 'oct 15' })))).toEqual([3])
  expect(ids(filterClusters(ALL, f({ query: '06:05' })))).toEqual([4])
})

test('the query matches any member’s file path', () => {
  expect(ids(filterClusters(ALL, f({ query: 'dscf01' })))).toEqual([1])
  expect(ids(filterClusters(ALL, f({ query: 'img_2290' })))).toEqual([3])
})

test('a query that matches nothing leaves nothing', () => {
  expect(filterClusters(ALL, f({ query: 'harbour' }))).toEqual([])
})

test('camera, query and sort combine', () => {
  expect(ids(filterClusters(ALL, f({ device: 'X-T5', query: 'dscf0', sort: 'time-desc' })))).toEqual([4, 3, 1])
})
