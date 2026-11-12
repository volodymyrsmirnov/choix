// Compare states 400–409: Focus' compare mode (v1 Focus.tsx), up to four
// panes sharing one zoom and pan. Wired like Focus (focus.tsx).
import { FocusScreen } from '../../src/design/screens/focus/FocusScreen'
import { RIDGE_CLUSTER, WITH_VIDEO } from '../mock/focus'
import type { Scenario } from '../registry'
import { focusProps, type FocusState } from './focus'

const V1 = 'v1:components/Focus.tsx'

const PANES = '[data-testid="compare-pane"]'
/** What each state's label names, which must be in view at every size. */
const SUBJECT: Record<number, string> = {
  400: PANES,
  401: PANES,
  402: PANES,
  403: '[data-testid="compare-pane"][data-active="true"] [data-testid="pane-mark"]',
  404: PANES,
  405: '[data-testid="hero-video"], [data-testid="compare-pane"] video',
  406: PANES,
  407: '[data-testid="compare-empty"]',
  408: '[data-testid="compare-pane"][data-active="true"] [data-testid="pane-mark"]',
  409: '[data-testid="pick-btn"]',
}

const compare = (n: number, label: string, s: Omit<FocusState, 'comparing'>): Scenario => ({
  n,
  group: 'compare',
  label,
  design: V1,
  subject: SUBJECT[n],
  render: (ctx) => <FocusScreen {...focusProps(ctx, { ...s, comparing: true })} />,
})

const RIDGE = RIDGE_CLUSTER

export const scenarios: Scenario[] = [
  compare(400, 'Two panes', { data: RIDGE, compareIdx: [0, 2], activeIndex: 0 }),
  compare(401, 'Three panes', { data: RIDGE, compareIdx: [0, 2, 5], activeIndex: 2 }),
  compare(402, 'Four panes', { data: RIDGE, compareIdx: [0, 2, 5, 4], activeIndex: 0 }),
  compare(403, 'Active pane picked', { data: RIDGE, compareIdx: [0, 1], activeIndex: 1 }),
  compare(404, 'Zoomed, panes in lockstep', {
    data: RIDGE,
    compareIdx: [0, 2, 6],
    activeIndex: 0,
    zoom: 2.25,
    pan: { x: -140, y: 60 },
  }),
  compare(405, 'Pane with video (S3)', {
    data: WITH_VIDEO,
    compareIdx: [0, 1],
    activeIndex: 1,
  }),
  compare(406, 'One pane', { data: RIDGE, compareIdx: [2], activeIndex: 2 }),
  compare(407, 'No panes selected', { data: RIDGE, compareIdx: [], activeIndex: 0 }),
  compare(408, 'Active pane rejected', { data: RIDGE, compareIdx: [2, 3], activeIndex: 3 }),
  compare(409, 'Pane with pick in flight', { data: RIDGE, compareIdx: [0, 2, 5], activeIndex: 2, busy: [4103] }),
]
