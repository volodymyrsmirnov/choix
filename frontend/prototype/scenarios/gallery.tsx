// Gallery states 800–809: every shared component in every look, for review.
// The gallery has no design reference of its own.
import type { CSSProperties, ReactNode } from 'react'
import { dialog } from '../../src/design/dialogs'
import { MENU_MANIFEST } from '../../src/design/menus'
import {
  AlertReplica,
  BackButton,
  Banner,
  Button,
  ClusterSection,
  ComparePane,
  ContextMenuReplica,
  Dot,
  Dropdown,
  EmptyState,
  Field,
  FocusRail,
  FolderRow,
  Icon,
  IconButton,
  ICON_NAMES,
  Kbd,
  MenuBarReplica,
  ProgressRail,
  Prose,
  QualityBadge,
  QualityIndicator,
  ScanIndicator,
  Slider,
  Spinner,
  Switch,
  TextInput,
  Thumb,
  TitleBar,
  type ButtonVariant,
  type IconName,
} from '../../src/design/components'
import type { MenuDef, MenuItemDef, QualityStatus, ScanProgress } from '../../src/design/types'
import { DOLOMITES, KYOTO, photo } from '../mock/data'
import { RIDGE } from '../mock/dolomites'
import { MEDIA } from '../mock/folder'
import type { Scenario, ScenarioCtx } from '../registry'

const MENUS = MENU_MANIFEST
const noop = () => {}
const removeCopy = dialog('remove-folder', { name: 'Atacama 2026' })

// ── Layout helpers (prototype only) ────────────────────────────────────────

function Page({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="h-full overflow-y-auto" data-scroll>
      <div className="px-8 pt-6 pb-16 flex flex-col gap-8">
        <h1 className="m-0 text-18 font-semibold tracking-tight">{title}</h1>
        {children}
      </div>
    </div>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-3">
      <h2 className="eyebrow m-0">{title}</h2>
      {children}
    </section>
  )
}

function Cell({ caption, children }: { caption: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5 items-start min-w-0">
      <div className="font-mono text-10 text-fg-3">{caption}</div>
      {children}
    </div>
  )
}

const LOOKS = [
  { caption: 'Rest' },
  { caption: 'Hover', force: 'hover' },
  { caption: 'Focus', force: 'focus' },
  { caption: 'Pressed', force: 'active' },
  { caption: 'Disabled', disabled: true },
  { caption: 'Busy', busy: true },
] as const

// Store helpers: scenario-local values, with a default until first set. A
// value that opens an overlay is keyed by that overlay's id, so a generic
// Escape handler can close the top overlay by setting its id to false.
function read<T>(ctx: ScenarioCtx, key: string, dflt: T): T {
  return key in ctx.store.state ? (ctx.store.state[key] as T) : dflt
}

// ── 800 Colours ────────────────────────────────────────────────────────────

const COLOURS: { group: string; items: { token: string; cls: string }[] }[] = [
  {
    group: 'Surfaces',
    items: [
      { token: 'bg-0', cls: 'bg-bg-0' },
      { token: 'bg-1', cls: 'bg-bg-1' },
      { token: 'bg-2', cls: 'bg-bg-2' },
      { token: 'bg-3', cls: 'bg-bg-3' },
      { token: 'bg-4', cls: 'bg-bg-4' },
      { token: 'bg-5', cls: 'bg-bg-5' },
    ],
  },
  {
    group: 'Text and lines',
    items: [
      { token: 'fg-0', cls: 'bg-fg-0' },
      { token: 'fg-1', cls: 'bg-fg-1' },
      { token: 'fg-2', cls: 'bg-fg-2' },
      { token: 'fg-3', cls: 'bg-fg-3' },
      { token: 'line-1', cls: 'bg-line-1' },
      { token: 'line-2', cls: 'bg-line-2' },
    ],
  },
  {
    group: 'Pick, reject, selection, warning',
    items: [
      { token: 'pick', cls: 'bg-pick' },
      { token: 'pick-hover', cls: 'bg-pick-hover' },
      { token: 'pick-ink', cls: 'bg-pick-ink' },
      { token: 'pick-bg', cls: 'bg-pick-bg' },
      { token: 'reject', cls: 'bg-reject' },
      { token: 'reject-ink', cls: 'bg-reject-ink' },
      { token: 'reject-bg', cls: 'bg-reject-bg' },
      { token: 'reject-badge', cls: 'bg-reject-badge' },
      { token: 'sel', cls: 'bg-sel' },
      { token: 'sel-bg', cls: 'bg-sel-bg' },
      { token: 'warn', cls: 'bg-warn' },
    ],
  },
  {
    group: 'Overlays and placeholders',
    items: [
      { token: 'scrim', cls: 'bg-scrim' },
      { token: 'backdrop', cls: 'bg-backdrop' },
      { token: 'toast', cls: 'bg-toast' },
      { token: 'panel', cls: 'bg-panel' },
      { token: 'kbd', cls: 'bg-kbd' },
      { token: 'kbd-in-btn', cls: 'bg-kbd-in-btn' },
      { token: 'spinner-track', cls: 'bg-spinner-track' },
      { token: 'imgph-a', cls: 'bg-imgph-a' },
      { token: 'imgph-b', cls: 'bg-imgph-b' },
      { token: 'art-from', cls: 'bg-art-from' },
      { token: 'art-to', cls: 'bg-art-to' },
    ],
  },
]

function Colours() {
  return (
    <Page title="Colours">
      {COLOURS.map((g) => (
        <Section key={g.group} title={g.group}>
          <div className="grid grid-cols-6 gap-3">
            {g.items.map((c) => (
              <div key={c.token} className="flex flex-col gap-1.5">
                <div className={`h-12 rounded-md border border-line-1 ${c.cls}`} />
                <div className="font-mono text-11 text-fg-1">--color-{c.token}</div>
              </div>
            ))}
          </div>
        </Section>
      ))}
      <Section title="Shadows and radii">
        <div className="flex flex-wrap gap-6">
          {[
            { name: 'shadow-pop', cls: 'shadow-pop' },
            { name: 'shadow-pick-glow', cls: 'shadow-pick-glow' },
            { name: 'shadow-pick-ring', cls: 'shadow-pick-ring' },
            { name: 'shadow-sel-ring', cls: 'shadow-sel-ring' },
            { name: 'shadow-sel-focus', cls: '[box-shadow:var(--shadow-sel-focus)]' },
            { name: 'shadow-sel-mark', cls: '[box-shadow:var(--shadow-sel-mark)]' },
            { name: 'shadow-sel-mark-focus', cls: '[box-shadow:var(--shadow-sel-mark-focus)]' },
            { name: 'shadow-art', cls: 'shadow-art' },
          ].map((s) => (
            <div key={s.name} className="flex flex-col gap-2 items-start">
              <div className={`w-24 h-16 rounded-md bg-bg-3 border border-line-2 ${s.cls}`} />
              <div className="font-mono text-11 text-fg-1">{s.name}</div>
            </div>
          ))}
          {[
            { name: 'radius-sm', cls: 'rounded-sm' },
            { name: 'radius-md', cls: 'rounded-md' },
            { name: 'radius-lg', cls: 'rounded-lg' },
            { name: 'radius-xl', cls: 'rounded-xl' },
          ].map((r) => (
            <div key={r.name} className="flex flex-col gap-2 items-start">
              <div className={`w-16 h-16 bg-bg-4 border border-line-2 ${r.cls}`} />
              <div className="font-mono text-11 text-fg-1">{r.name}</div>
            </div>
          ))}
        </div>
      </Section>
    </Page>
  )
}

// ── 801 Type scale ─────────────────────────────────────────────────────────

const SIZES = [
  { n: 9, cls: 'text-9' },
  { n: 10, cls: 'text-10' },
  { n: 11, cls: 'text-11' },
  { n: 12, cls: 'text-12' },
  { n: 13, cls: 'text-13' },
  { n: 14, cls: 'text-14' },
  { n: 15, cls: 'text-15' },
  { n: 17, cls: 'text-17' },
  { n: 18, cls: 'text-18' },
  { n: 20, cls: 'text-20' },
  { n: 22, cls: 'text-22' },
  { n: 30, cls: 'text-30' },
]

function TypeScale() {
  return (
    <Page title="Type scale">
      <Section title="Inter and JetBrains Mono, by size">
        <div className="flex flex-col gap-2">
          {SIZES.map((s) => (
            <div key={s.n} className="grid grid-cols-[72px_minmax(0,1fr)_minmax(0,0.8fr)] gap-4 items-baseline">
              <span className="font-mono text-11 text-fg-3">text-{s.n}</span>
              <span className={`${s.cls} text-fg-0 truncate`}>Atacama — 2,617 photos</span>
              {/* At text-30 the mono face's ascent and descent exceed the 36 px
                  line, and truncate would clip the underscore: that size only
                  takes leading-normal. */}
              <span className={`${s.cls} font-mono text-fg-1 truncate ${s.n === 30 ? 'leading-normal' : ''}`}>IMG_0412.RAF</span>
            </div>
          ))}
        </div>
      </Section>
      <Section title="Weights at text-14">
        <div className="flex flex-wrap gap-8 text-14">
          <span className="font-normal">Regular 400</span>
          <span className="font-medium">Medium 500</span>
          <span className="font-semibold">Semibold 600</span>
          <span className="font-bold">Bold 700</span>
          <span className="font-mono font-normal">Mono 400</span>
          <span className="font-mono font-medium">Mono 500</span>
        </div>
      </Section>
      <Section title="Text tiers and the eyebrow">
        <div className="flex flex-col gap-1.5 text-13">
          <span className="text-fg-0">fg-0 — titles and values</span>
          <span className="text-fg-1">fg-1 — body copy</span>
          <span className="text-fg-2">fg-2 — secondary copy and hints</span>
          <span className="text-fg-3">fg-3 — the faintest readable label</span>
          <span className="eyebrow mt-2">October 2026</span>
        </div>
      </Section>
      <Section title="Prose, with the last two words bound">
        <div className="max-w-[360px] text-13 text-fg-1 leading-[1.6]">
          <Prose wrap="pretty">
            Choix reads your photos where they are. Nothing is uploaded, moved or changed, and your photos never leave it.
          </Prose>
        </div>
      </Section>
    </Page>
  )
}

// ── 802 Buttons ────────────────────────────────────────────────────────────

const BUTTONS: { variant: ButtonVariant; label: string; icon?: IconName; kbd?: string }[] = [
  { variant: 'default', label: 'Rescan', icon: 'refresh' },
  { variant: 'ghost', label: 'Camera', icon: 'filter' },
  { variant: 'primary', label: 'Add Folder', icon: 'plus' },
  { variant: 'pick', label: 'Pick', icon: 'check', kbd: 'Space' },
  { variant: 'reject', label: 'Reject', icon: 'x', kbd: 'X' },
]

const BUTTON_GRID = 'grid grid-cols-[72px_repeat(6,max-content)] gap-x-4 gap-y-3 items-center'

function Buttons() {
  return (
    <Page title="Buttons">
      <Section title="Variants and looks">
        <div className={BUTTON_GRID}>
          <span />
          {LOOKS.map((l) => (
            <span key={l.caption} className="font-mono text-10 text-fg-3">
              {l.caption}
            </span>
          ))}
          {BUTTONS.map((b) => (
            <ButtonRow key={b.variant} b={b} />
          ))}
        </div>
      </Section>
      <Section title="Icon buttons">
        <div className={BUTTON_GRID}>
          <span className="font-mono text-11 text-fg-2">ghost</span>
          {LOOKS.map((l) =>
            'busy' in l ? (
              <span key={l.caption} />
            ) : (
              <span key={l.caption}>
                <IconButton
                  icon="gear"
                  label="Settings"
                  onClick={noop}
                  force={'force' in l ? l.force : undefined}
                  disabled={'disabled' in l}
                />
              </span>
            ),
          )}
          <span className="font-mono text-11 text-fg-2">default</span>
          {LOOKS.map((l) =>
            'busy' in l ? (
              <span key={l.caption} />
            ) : (
              <span key={l.caption}>
                <IconButton
                  icon="dots"
                  label="More actions"
                  variant="default"
                  onClick={noop}
                  force={'force' in l ? l.force : undefined}
                  disabled={'disabled' in l}
                />
              </span>
            ),
          )}
        </div>
      </Section>
      <Section title="Without an icon, with a key cap">
        <div className="flex flex-wrap items-center gap-3">
          <Button variant="default" onClick={noop}>
            Cancel
          </Button>
          <Button variant="primary" onClick={noop}>
            Save
          </Button>
          <Button variant="primary" icon="plus" kbd="⌘O" onClick={noop}>
            Add Folder
          </Button>
          <Button variant="ghost" kbd="Esc" onClick={noop}>
            Close
          </Button>
          <Button variant="default" icon="folder" kbd="⌘P" onClick={noop}>
            Open Picks
          </Button>
          <Button variant="default" className="text-reject" onClick={noop}>
            Discard changes
          </Button>
        </div>
      </Section>
    </Page>
  )
}

function ButtonRow({ b }: { b: (typeof BUTTONS)[number] }) {
  return (
    <>
      <span className="font-mono text-11 text-fg-2">{b.variant}</span>
      {LOOKS.map((l) => (
        <span key={l.caption}>
          <Button
            variant={b.variant}
            icon={b.icon}
            kbd={b.kbd}
            onClick={noop}
            force={'force' in l ? l.force : undefined}
            disabled={'disabled' in l}
            busy={'busy' in l}
          >
            {b.label}
          </Button>
        </span>
      ))}
    </>
  )
}

// ── 803 Inputs and switches ────────────────────────────────────────────────

const CAMERAS = [
  { value: '', label: 'All cameras' },
  { value: 'X-T5', label: 'X-T5' },
  { value: 'iPhone 15 Pro', label: 'iPhone 15 Pro' },
]

function Inputs({ ctx }: { ctx: ScenarioCtx }) {
  const set = ctx.store.set
  const text = read(ctx, 'text', 'picks')
  const query = read(ctx, 'query', '')
  const minutes = read(ctx, 'minutes', '10')
  const on = read(ctx, 'on', true)
  const off = read(ctx, 'off', false)
  const ddOpen = read(ctx, 'gallery-dropdown', true)
  const camera = read(ctx, 'camera', '')
  const sim = read(ctx, 'sim', 0.92)
  const cameraLabel = CAMERAS.find((c) => c.value === camera)?.label ?? camera
  return (
    <Page title="Inputs and switches">
      <Section title="Dropdown">
        <div className="flex items-start gap-10 min-h-[160px]">
          {/* Every dropdown here shares one overlay id, so the gallery
              registers one overlay: the open one. */}
          <Cell caption="Hover">
            <Dropdown id="gallery-dropdown" label="All cameras" icon="filter" srPrefix="Camera:" options={CAMERAS} value="" open={false} onOpenChange={noop} onSelect={noop} force="hover" />
          </Cell>
          <Cell caption="Focus">
            <Dropdown id="gallery-dropdown" label="All cameras" icon="filter" srPrefix="Camera:" options={CAMERAS} value="" open={false} onOpenChange={noop} onSelect={noop} force="focus" />
          </Cell>
          <Cell caption="Disabled">
            <Dropdown id="gallery-dropdown" label="All cameras" icon="filter" srPrefix="Camera:" options={CAMERAS} value="" open={false} onOpenChange={noop} onSelect={noop} disabled />
          </Cell>
          <Cell caption="Open, X-T5 hovered">
            <Dropdown
              id="gallery-dropdown"
              label={cameraLabel}
              icon="filter"
              srPrefix="Camera:"
              menuLabel="Camera"
              options={CAMERAS}
              value={camera}
              open={ddOpen}
              onOpenChange={(v) => set({ 'gallery-dropdown': v })}
              onSelect={(v) => set({ camera: v })}
              forceItem={{ id: 'X-T5', force: 'hover' }}
            />
          </Cell>
        </div>
      </Section>
      <Section title="Text field">
        <div className="grid grid-cols-5 gap-4">
          <Cell caption="Rest">
            <TextInput label="Rest" value={text} onChange={(v) => set({ text: v })} className="w-full" />
          </Cell>
          <Cell caption="Hover">
            <TextInput label="Hover" value={text} onChange={(v) => set({ text: v })} className="w-full" force="hover" />
          </Cell>
          <Cell caption="Focus">
            <TextInput
              label="Focus"
              value={text}
              onChange={(v) => set({ text: v })}
              className="w-full"
              force="focus-within"
            />
          </Cell>
          <Cell caption="Invalid">
            <TextInput label="Invalid" value="/tmp/picks" onChange={noop} className="w-full" invalid mono />
          </Cell>
          <Cell caption="Disabled">
            <TextInput label="Disabled" value={text} onChange={noop} className="w-full" disabled />
          </Cell>
        </div>
      </Section>
      <Section title="Search and number fields">
        <div className="flex flex-wrap items-center gap-6">
          <TextInput
            label="Filter clusters"
            type="search"
            icon="search"
            size="sm"
            placeholder="Filter by name or camera"
            value={query}
            onChange={(v) => set({ query: v })}
            className="w-[240px]"
          />
          <div className="flex items-center gap-2.5">
            <TextInput
              label="Time bucket in minutes"
              type="number"
              mono
              align="right"
              value={minutes}
              onChange={(v) => set({ minutes: v })}
              className="w-24"
            />
            <span className="text-12 text-fg-2">minutes</span>
          </div>
        </div>
      </Section>
      <Section title="Switch">
        <div className="grid grid-cols-[72px_repeat(5,minmax(0,1fr))] gap-x-3 gap-y-3 items-center">
          <span />
          {LOOKS.slice(0, 5).map((l) => (
            <span key={l.caption} className="font-mono text-10 text-fg-3">
              {l.caption}
            </span>
          ))}
          {[
            { name: 'off', checked: off, key: 'off' },
            { name: 'on', checked: on, key: 'on' },
          ].map((row) => (
            <SwitchRow key={row.name} row={row} onChange={(v) => set({ [row.key]: v })} />
          ))}
        </div>
      </Section>
      <Section title="Slider">
        <div className="grid grid-cols-5 gap-4">
          {LOOKS.slice(0, 5).map((l) => (
            <Cell key={l.caption} caption={l.caption}>
              <Slider
                id={`g-slider-${l.caption.toLowerCase()}`}
                label={`Visual similarity, ${l.caption}`}
                value={sim}
                min={0.05}
                max={1}
                step={0.01}
                onChange={(v) => set({ sim: v })}
                format={(v) => v.toFixed(2)}
                force={'force' in l ? l.force : undefined}
                disabled={'disabled' in l}
                className="w-full"
              />
            </Cell>
          ))}
        </div>
      </Section>
      <Section title="Settings fields">
        <div className="flex flex-col gap-6 max-w-[640px]">
          <Field
            controlId="g-advance"
            title="Advance on action"
            hint="Cycle to the next photo in the cluster after a Pick or Reject."
            aside={
              <div className="flex items-center gap-3">
                <Switch
                  id="g-advance"
                  labelledBy="g-advance-label"
                  describedBy="g-advance-hint"
                  checked={on}
                  onChange={(v) => set({ on: v })}
                />
                <span aria-hidden="true" className="text-12 text-fg-2">
                  {on ? 'On' : 'Off'}
                </span>
              </div>
            }
          />
          <Field
            controlId="g-picks"
            title="Picks destination"
            hint="A folder name inside the scanned folder, e.g. picks."
            error="Use a folder name inside the scanned folder, not an absolute path."
          >
            <TextInput
              id="g-picks"
              value="/tmp/picks"
              onChange={noop}
              invalid
              mono
              className="w-full"
              aria-describedby="g-picks-hint g-picks-error"
            />
          </Field>
        </div>
      </Section>
    </Page>
  )
}

function SwitchRow({ row, onChange }: { row: { name: string; checked: boolean }; onChange: (v: boolean) => void }) {
  return (
    <>
      <span className="font-mono text-11 text-fg-2">{row.name}</span>
      {LOOKS.slice(0, 5).map((l) => (
        <span key={l.caption}>
          <Switch
            label={`${row.name}, ${l.caption}`}
            checked={row.checked}
            onChange={onChange}
            force={'force' in l ? l.force : undefined}
            disabled={'disabled' in l}
          />
        </span>
      ))}
    </>
  )
}

// ── 804 Thumbnails ─────────────────────────────────────────────────────────

function Thumbnails() {
  const states = [
    { caption: 'Rest', state: undefined, current: false },
    { caption: 'Picked', state: 'picked', current: false },
    { caption: 'Rejected', state: 'rejected', current: false },
    { caption: 'Current', state: undefined, current: true },
    { caption: 'Current, picked', state: 'picked', current: true },
    { caption: 'Current, rejected', state: 'rejected', current: true },
  ] as const
  return (
    <Page title="Thumbnails">
      <Section title="States">
        <div className="flex flex-wrap gap-6">
          {states.map((s) => (
            <Cell key={s.caption} caption={s.caption}>
              <Thumb src={photo(8)} alt="IMG_0412.HEIC" state={s.state} current={s.current} size={116} />
            </Cell>
          ))}
        </div>
      </Section>
      <Section title="As a button">
        <div className="flex flex-wrap gap-6">
          {[
            { caption: 'Rest', force: undefined },
            { caption: 'Hover', force: 'hover' },
            { caption: 'Focus', force: 'focus' },
            { caption: 'Pressed', force: 'active' },
          ].map((s) => (
            <Cell key={s.caption} caption={s.caption}>
              <Thumb src={photo(8)} alt="IMG_0412.HEIC" size={116} onClick={noop} force={s.force} />
            </Cell>
          ))}
        </div>
      </Section>
      <Section title="Badges">
        <div className="flex flex-wrap gap-6">
          <Cell caption="Quality">
            <Thumb src={photo(9)} alt="IMG_0413.HEIC" quality={6.4} size={116} onClick={noop} />
          </Cell>
          <Cell caption="Video">
            <Thumb src={photo(31)} alt="MOV_0007.MOV" video size={116} onClick={noop} />
          </Cell>
          <Cell caption="Picked, quality">
            <Thumb src={photo(10)} alt="IMG_0414.HEIC" state="picked" quality={8.1} size={116} onClick={noop} />
          </Cell>
          <Cell caption="Rejected video">
            <Thumb src={photo(32)} alt="MOV_0008.MOV" state="rejected" video size={116} onClick={noop} />
          </Cell>
          <Cell caption="Rail size, current">
            <Thumb src={photo(11)} alt="IMG_0415.HEIC" current size={56} onClick={noop} />
          </Cell>
        </div>
      </Section>
    </Page>
  )
}

// ── 805 Badges, dots, progress, spinner ────────────────────────────────────

const SCANS: { caption: string; scan: ScanProgress }[] = [
  { caption: 'Discovering', scan: { stage: 'discover', done: 0, total: 0, failed: 0, state: 'running' } },
  { caption: 'Reading metadata', scan: { stage: 'metadata', done: 1377, total: 2617, failed: 0, state: 'running' } },
  { caption: 'Thumbnails, failures', scan: { stage: 'thumb', done: 2206, total: 2617, failed: 7, state: 'running' } },
  { caption: 'Stopped', scan: { stage: 'analyze', done: 951, total: 2617, failed: 0, state: 'stopped' } },
]

const quality = (q: Partial<QualityStatus>): QualityStatus => ({
  enabled: true,
  state: 'idle',
  active: true,
  received: 0,
  size: 0,
  done: 0,
  total: 0,
  rated: 0,
  ...q,
})

const QUALITIES: { caption: string; status: QualityStatus }[] = [
  { caption: 'Downloading', status: quality({ state: 'downloading', received: 88_000_000, size: 177_000_000 }) },
  { caption: 'Downloading, size unknown', status: quality({ state: 'downloading' }) },
  { caption: 'Rating', status: quality({ state: 'scoring', done: 640, total: 2310 }) },
  { caption: 'Download failed', status: quality({ state: 'download_failed', error: 'connection reset' }) },
  { caption: 'Stopped', status: quality({ state: 'error', error: 'model failed to load' }) },
]

function Feedback() {
  return (
    <Page title="Badges, dots, progress, spinner">
      <Section title="Quality badge, dots, key caps">
        <div className="flex flex-wrap items-center gap-6">
          <QualityBadge value={0} standalone />
          <QualityBadge value={6.44} standalone />
          <QualityBadge value={9.8} standalone />
          <span className="flex items-center gap-2 text-12 text-fg-2">
            <Dot /> neutral
          </span>
          <span className="flex items-center gap-2 text-12 text-fg-2">
            <Dot tone="pick" /> pick
          </span>
          <span className="flex items-center gap-2 text-12 text-fg-2">
            <Dot tone="reject" /> reject
          </span>
          <span className="flex items-center gap-2 text-12 text-fg-2">
            <Dot tone="warn" /> warn
          </span>
          <span className="flex items-center gap-2 text-12 text-fg-2">
            <Dot tone="active" /> active
          </span>
          <span className="flex items-center gap-1">
            {['⌘', '⇧', '⌥', '⌃', '↩', '⌫', 'Space', 'Esc', '?'].map((k) => (
              <Kbd key={k}>{k}</Kbd>
            ))}
          </span>
        </div>
      </Section>
      <Section title="Progress and spinner">
        <div className="grid grid-cols-4 gap-6 items-center">
          <Cell caption="0 %">
            <ProgressRail className="w-40" label="Empty" value={0} />
          </Cell>
          <Cell caption="42 %">
            <ProgressRail className="w-40" label="Part" value={0.42} />
          </Cell>
          <Cell caption="100 %">
            <ProgressRail className="w-40" label="Full" value={1} />
          </Cell>
          <Cell caption="Indeterminate">
            <ProgressRail className="w-40" label="Working" />
          </Cell>
          <Cell caption="Spinner, small">
            <Spinner small />
          </Cell>
          <Cell caption="Spinner">
            <Spinner />
          </Cell>
        </div>
      </Section>
      <Section title="Scan indicator">
        <div className="grid grid-cols-2 gap-x-8 gap-y-4">
          {SCANS.map((s) => (
            <Cell key={s.caption} caption={s.caption}>
              <ScanIndicator scan={s.scan} />
            </Cell>
          ))}
        </div>
      </Section>
      <Section title="Quality indicator">
        <div className="grid grid-cols-2 gap-x-8 gap-y-4">
          {QUALITIES.map((q) => (
            <Cell key={q.caption} caption={q.caption}>
              <QualityIndicator status={q.status} />
            </Cell>
          ))}
        </div>
      </Section>
      <Section title="Banners">
        <div className="flex flex-col gap-3 max-w-[640px]">
          <Banner tone="info" icon="info" title="Downloading the visual grouping model" body="Folders are grouped by time until it arrives.">
            <ProgressRail className="w-full mt-2" label="Visual grouping model download" value={0.31} />
          </Banner>
          <Banner
            tone="warn"
            icon="alert"
            title="Grouped by time only"
            body="Turn on visual grouping in Settings to group similar shots."
            actions={
              <Button variant="default" onClick={noop}>
                Settings
              </Button>
            }
          />
          <Banner
            tone="error"
            icon="drive"
            title="Drive “Card A” is not connected"
            body="Connect it again to open this folder."
          />
        </div>
      </Section>
      <Section title="Empty state">
        <div className="flex justify-center rounded-lg border border-line-1 bg-bg-0">
          <EmptyState
            title="No photos or videos found"
            body="Choix found nothing it can show in this folder. Add photos or videos to it, then rescan."
          >
            <Button variant="default" icon="refresh" onClick={noop}>
              Rescan
            </Button>
          </EmptyState>
        </div>
      </Section>
      <Section title="Icons">
        <div className="flex flex-wrap gap-4 text-fg-1">
          {ICON_NAMES.map((name) => (
            <span key={name} className="flex flex-col items-center gap-1.5 w-[88px]">
              <Icon name={name} size={16} />
              <span className="font-mono text-10 text-fg-3 whitespace-nowrap">{name}</span>
            </span>
          ))}
        </div>
      </Section>
    </Page>
  )
}

// ── 806 Menu and alert replicas ────────────────────────────────────────────

function Replicas({ ctx }: { ctx: ScenarioCtx }) {
  const set = ctx.store.set
  // One key per menu, `menu:<id>`, the overlay id that menu registers.
  const menu = MENUS.menus.find((m) => read(ctx, `menu:${m.id}`, m.id === 'file'))?.id ?? null
  const openMenu = (id: string | null) =>
    set(Object.fromEntries(MENUS.menus.map((m) => [`menu:${m.id}`, m.id === id])))
  const contexts = [
    { id: 'gallery-context', caption: 'Folder row, Open focused', flags: [] as string[] },
    { id: 'gallery-context-offline', caption: 'Offline folder', flags: ['offline'] },
    { id: 'gallery-context-scanning', caption: 'Scanning folder', flags: ['scanning'] },
  ]
  const alert = read(ctx, 'gallery-alert', true)
  const looks = read(ctx, 'gallery-alert-looks', true)
  return (
    <Page title="Menu and alert replicas">
      <Section title="Menu bar, Library, a folder selected (Edit hovered, Open Picks Folder highlighted)">
        <div className="h-[300px] rounded-lg border border-line-1 bg-bg-1">
          <MenuBarReplica
            menus={MENUS.menus}
            open={menu}
            screen="library"
            hasSelection
            onOpen={openMenu}
            onItem={() => openMenu(null)}
            forceTitle={{ id: 'edit', force: 'hover' }}
            forceItem={{ id: 'open-picks', force: 'hover' }}
          />
        </div>
      </Section>
      <Section title="Context menus">
        <div className="flex flex-wrap items-start gap-8">
          {contexts.map((c) =>
            read(ctx, c.id, true) ? (
              <Cell key={c.id} caption={c.caption}>
                <ContextMenuReplica
                  id={c.id}
                  label="Folder actions"
                  items={MENUS.context['folder-row'] ?? []}
                  flags={c.flags}
                  onItem={() => set({ [c.id]: false })}
                  forceItem={c.id === 'gallery-context' ? { id: 'open-folder', force: 'focus' } : undefined}
                />
              </Cell>
            ) : null,
          )}
        </div>
      </Section>
      <Section title="Alert">
        {alert && (
          <AlertReplica
            id="gallery-alert"
            title={removeCopy.title}
            message={removeCopy.message}
            buttons={removeCopy.buttons.map((b) => ({ ...b, onClick: () => set({ 'gallery-alert': false }) }))}
          />
        )}
      </Section>
      <Section title="Alert buttons: rest, hover, pressed, focus; one button spans the width">
        <div className="flex flex-wrap items-start gap-8">
          {looks && (
            <AlertReplica
              id="gallery-alert-looks"
              title="Button looks"
              message="The default button at rest, hovered, pressed and focused."
              buttons={[
                { label: 'Rest', role: 'default', onClick: () => set({ 'gallery-alert-looks': false }) },
                { label: 'Hover', role: 'default', force: 'hover' },
                { label: 'Pressed', role: 'default', force: 'active' },
                { label: 'Focus', role: 'default', force: 'focus' },
              ]}
            />
          )}
          {read(ctx, 'gallery-alert-single', true) && (
            <AlertReplica
              id="gallery-alert-single"
              title="Choix could not open “Card A”"
              message="The drive is not connected."
              buttons={[{ label: 'OK', role: 'default', onClick: () => set({ 'gallery-alert-single': false }) }]}
            />
          )}
        </div>
      </Section>
    </Page>
  )
}

// ── 807 Title bar variants ─────────────────────────────────────────────────

function Bar({ caption, children }: { caption: string; children: ReactNode }) {
  return (
    <Cell caption={caption}>
      <div className="w-full rounded-lg border border-line-1 overflow-hidden" data-clip="intended">
        {children}
      </div>
    </Cell>
  )
}

const libraryTrailing = (
  <>
    <Button variant="default" icon="plus" kbd="⌘O" onClick={noop}>
      Add Folder
    </Button>
    <IconButton icon="help" label="Keyboard shortcuts" onClick={noop} aria-haspopup="dialog" />
    <IconButton icon="gear" label="Settings" onClick={noop} />
  </>
)

const folderTrailing = (
  <>
    <Button variant="default" icon="folder" kbd="⌘P" onClick={noop}>
      Open Picks
    </Button>
    <IconButton icon="help" label="Keyboard shortcuts" onClick={noop} aria-haspopup="dialog" />
    <IconButton icon="gear" label="Settings" onClick={noop} />
  </>
)

const back = <BackButton to="Library" onClick={noop} />

function TitleBars() {
  return (
    <Page title="Title bar variants">
      <div className="flex flex-col gap-5">
        <Bar caption="Library">
          <TitleBar alignGroup="titlebar-1" title="Library" titleHidden trailing={libraryTrailing} />
        </Bar>
        <Bar caption="Folder, scanning">
          <TitleBar
            alignGroup="titlebar-2"
            leading={back}
            title="Atacama — Salt Flats"
            subtitle="1,318 photos · 274 clusters · 41 picked"
            indicator={<ScanIndicator scan={SCANS[1]!.scan} />}
            trailing={folderTrailing}
          />
        </Bar>
        <Bar caption="Folder, discovering">
          <TitleBar
            alignGroup="titlebar-3"
            leading={back}
            title="Atacama — Salt Flats"
            subtitle="Scanning…"
            indicator={<ScanIndicator scan={SCANS[0]!.scan} />}
            trailing={folderTrailing}
          />
        </Bar>
        <Bar caption="Folder, quality model downloading">
          <TitleBar
            alignGroup="titlebar-4"
            leading={back}
            title="Atacama — Salt Flats"
            subtitle="1,318 photos · 274 clusters · 41 picked"
            indicator={<QualityIndicator status={QUALITIES[0]!.status} />}
            trailing={folderTrailing}
          />
        </Bar>
        <Bar caption="Folder, scan stopped">
          <TitleBar
            alignGroup="titlebar-5"
            leading={back}
            title="Atacama — Salt Flats"
            subtitle="1,318 photos · 274 clusters"
            indicator={<ScanIndicator scan={SCANS[3]!.scan} />}
            trailing={folderTrailing}
          />
        </Bar>
        <Bar caption="Long folder name">
          <TitleBar
            alignGroup="titlebar-6"
            leading={back}
            title="2026-03-22 Atacama, Uyuni and the long way round to San Pedro de Atacama (final)"
            subtitle="2,617 photos · 588 clusters · 164 picked · 97 rejected"
            trailing={folderTrailing}
          />
        </Bar>
        <Bar caption="Full screen (no traffic-light inset)">
          <TitleBar alignGroup="titlebar-7" fullScreen title="Library" titleHidden trailing={libraryTrailing} />
        </Bar>
      </div>
    </Page>
  )
}

// ── 808 Title bar in a real window ─────────────────────────────────────────
//
// What the app sets on its root once it has measured the window's buttons
// (macOS 27.2, hidden-inset title bar): the buttons' vertical middle is 26 px
// from the top of the window and they end at x = 79 px, so the bar's content
// starts 12 px after them (91 px). The traffic lights are drawn where macOS
// puts them (14 px buttons 6 px apart, 19 px from the top, the last ending at
// 79 px) and share the bar's alignment group, so the state checks hold their
// middle to the bar's slots.

const MEASURED = { '--titlebar-center': '26px', '--titlebar-inset': '91px' } as CSSProperties

function TrafficLights({ alignGroup }: { alignGroup: string }) {
  return (
    <div aria-hidden="true" data-align-y={alignGroup} className="absolute left-[25px] top-[19px] flex gap-[6px]">
      <span className="block w-[14px] h-[14px] rounded-full bg-reject" />
      <span className="block w-[14px] h-[14px] rounded-full bg-warn" />
      <span className="block w-[14px] h-[14px] rounded-full bg-pick" />
    </div>
  )
}

function WindowBar({ caption, alignGroup, children }: { caption: string; alignGroup: string; children: ReactNode }) {
  return (
    <Cell caption={caption}>
      <div className="relative w-full rounded-lg border border-line-1 overflow-hidden" data-clip="intended" style={MEASURED}>
        {children}
        <TrafficLights alignGroup={alignGroup} />
      </div>
    </Cell>
  )
}

function MeasuredTitleBars() {
  return (
    <Page title="Title bar in a macOS 27 window">
      <div className="flex flex-col gap-5">
        <WindowBar caption="Library (buttons centred 26 px from the top, ending at 79 px; content from 91 px)" alignGroup="measured-1">
          <TitleBar
            alignGroup="measured-1"
            title="Library"
            titleHidden
            trailing={
              <>
                <Button variant="primary" icon="plus" kbd="⌘O" onClick={noop}>
                  Add Folder
                </Button>
                <IconButton icon="help" label="Keyboard shortcuts" onClick={noop} aria-haspopup="dialog" />
                <IconButton icon="gear" label="Settings" onClick={noop} />
              </>
            }
          />
        </WindowBar>
        <WindowBar caption="Folder, scanning" alignGroup="measured-2">
          <TitleBar
            alignGroup="measured-2"
            leading={back}
            title="Atacama — Salt Flats"
            subtitle="1,318 photos · 274 clusters · 41 picked"
            indicator={<ScanIndicator scan={SCANS[1]!.scan} />}
            trailing={folderTrailing}
          />
        </WindowBar>
        <WindowBar caption="Settings" alignGroup="measured-3">
          <TitleBar
            alignGroup="measured-3"
            leading={<BackButton to="Library" onClick={noop} />}
            title="Settings"
            trailing={<IconButton icon="help" label="Keyboard shortcuts" onClick={noop} aria-haspopup="dialog" />}
          />
        </WindowBar>
      </div>
    </Page>
  )
}

// ── 809 The current item, one look everywhere ──────────────────────────────

/**
 * The owner's rule: the Library's selected row is the look of "the current
 * item" on every screen (components/current.ts). Keyboard focus on a current
 * item deepens its ring and draws no outline on top; a control that is not
 * an item keeps the focus ring. Picked and rejected stay marks: a current
 * photo that is picked keeps its green edge, ringed in blue.
 */
function CurrentItems() {
  const ridge = RIDGE.members.slice(0, 4)
  const counts = { picked: 1, rejected: 1, reviewed: 2 }
  const view = { zoom: 1, pan: { x: 0, y: 0 } }
  return (
    <Page title="The current item">
      <Section title="Library row">
        <div className="flex flex-col gap-6">
          <Cell caption="Current">
            <div className="w-full">
              <FolderRow folder={DOLOMITES} selected onOpen={noop} onSelect={noop} onOpenPicks={noop} onMenu={noop} />
            </div>
          </Cell>
          <Cell caption="Current, keyboard focus">
            <div className="w-full">
              <FolderRow folder={KYOTO} selected force="focus" onOpen={noop} onSelect={noop} onOpenPicks={noop} onMenu={noop} />
            </div>
          </Cell>
        </div>
      </Section>
      <Section title="Cluster on the Folder page">
        <div className="pl-2 pt-2">
          <ClusterSection cluster={{ ...RIDGE, members: ridge }} active counts={counts} media={MEDIA} qualityOn onSelect={noop} onOpen={noop} />
        </div>
      </Section>
      <div className="grid grid-cols-[132px_1fr] gap-8">
        <Section title="Focus rail">
          {/* The second photo is current and picked; the fourth is rejected. */}
          <div className="h-[300px] flex border border-line-1 rounded-md overflow-hidden" data-clip="intended">
            <FocusRail members={ridge} activeIndex={1} comparing={false} compareIdx={[]} rating={() => undefined} media={MEDIA} onItem={noop} />
          </div>
        </Section>
        <Section title="Compare pane, active">
          <div className="h-[300px] max-w-[480px] grid">
            <ComparePane
              member={ridge[0]!}
              pane={0}
              index={0}
              total={ridge.length}
              active
              rating={undefined}
              qualityOn={false}
              underZoom={false}
              zoomable
              media={MEDIA}
              state="ready"
              view={view}
              zoomBy={(v) => v}
              onView={noop}
              onActivate={noop}
              onMediaState={noop}
            />
          </div>
        </Section>
      </div>
      <Section title="Thumbnail button">
        <div className="flex flex-wrap gap-6">
          <Cell caption="Current, keyboard focus">
            <Thumb src={photo(8)} alt="IMG_0412.HEIC" current force="focus" size={116} onClick={noop} />
          </Cell>
          <Cell caption="Current and picked, keyboard focus">
            <Thumb src={photo(10)} alt="IMG_0414.HEIC" state="picked" current force="focus" size={116} onClick={noop} />
          </Cell>
          <Cell caption="Not current, keyboard focus (a control)">
            <Thumb src={photo(9)} alt="IMG_0413.HEIC" force="focus" size={116} onClick={noop} />
          </Cell>
        </div>
      </Section>
    </Page>
  )
}

// ── Registry entries ───────────────────────────────────────────────────────

const g = { group: 'gallery', design: 'missing design' } as const

export const scenarios: Scenario[] = [
  { n: 800, ...g, label: 'Colours', render: () => <Colours /> },
  { n: 801, ...g, label: 'Type scale', render: () => <TypeScale /> },
  { n: 802, ...g, label: 'Buttons (rest, hover, focus, pressed, disabled, busy)', render: () => <Buttons /> },
  {
    n: 803,
    ...g,
    label: 'Inputs and switches',
    overlays: ['gallery-dropdown'],
    render: (ctx) => <Inputs ctx={ctx} />,
  },
  { n: 804, ...g, label: 'Thumbnails (rest, picked, rejected, current, current and picked or rejected)', render: () => <Thumbnails /> },
  { n: 805, ...g, label: 'Badges, dots, progress, spinner', render: () => <Feedback /> },
  {
    n: 806,
    ...g,
    label: 'Menu and alert replicas',
    overlays: [
      'menu:file',
      'gallery-context',
      'gallery-context-offline',
      'gallery-context-scanning',
      'gallery-alert',
      'gallery-alert-looks',
      'gallery-alert-single',
    ],
    render: (ctx) => <Replicas ctx={ctx} />,
  },
  { n: 807, ...g, label: 'Title bar variants, including full screen (no traffic-light inset)', render: () => <TitleBars /> },
  { n: 808, ...g, label: 'Title bar in a macOS 27 window, centred on the traffic lights (drawn)', render: () => <MeasuredTitleBars /> },
  {
    n: 809,
    ...g,
    label: 'The current item, one look everywhere (row, cluster, rail photo, compare pane; picked; keyboard focus)',
    render: () => <CurrentItems />,
  },
]
