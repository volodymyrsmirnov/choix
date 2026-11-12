---
branch: feat/choix-2-backend-9051c1
states: [11-17, 30-37, 40, 100-131, 200-231, 300-308, 310-318, 400-409, 500-520, 600-605, 700-712, 720, 800-809]
retired: []
ui: true
---

Choix 2.0's engine and Wails shell. The branch also adds the app that mounts
the approved design; that part is written after this file, by the gated tasks
of the backend plan (Phase 6): the native window, menu and dialogs
(`internal/app/window`, `internal/app/menu`, `internal/app/dialogs`), the app
entry and shell (`frontend/index.html`, `frontend/src/main.tsx`,
`frontend/src/app/**`) and the containers that feed the shared screens from
bindings and events. Until those tasks land, none of these files exist.

It carries the design branch
(`feat/choix-2-desktop-design-463af0`), on which every registered state is
new, and reaches `main` together with it, so every state is listed here. No
state is retired.

The owner's first look at the real window (round 1) changes the design:
every title bar centres its content on the window's buttons, through the
inherited custom property `--titlebar-center` the app measures (default
23.5 px: the approved 48 px bar); Settings rows span the window's width,
with the controls in a column at the right and the save bar across the
pane; the app surface selects no text and drags no image, as a native
window (text fields and the error details stay selectable). New states:
40, the full-window error screen (a screen failed to render and the return
to the Library failed too), and 808, the title bar in a macOS 27 window
next to drawn traffic lights.

Round 2 (the engine runs two folder jobs at once and says which folder a
rating pass belongs to): the Library title bar counts several scans or
rating passes ("2 folders") and names the folder of a single rating pass;
the Folder title bar shows only that folder's work (nothing during its
first scan, whose checklist is the progress; never another folder's
rating pass); the Cluster stage is counted like the others; and a title
bar's indicator never shrinks, so a long title yields first. New states:
37 (two folders rating quality), 123 (two folders scanning, one queued),
230 (first scan, clustering, another folder rating) and 231 (long name,
scan running).

Round 4 (no duplicate status, the owner's rule: a status lives on the
element it describes, and a title bar shows one only when nothing else on
its screen does): the Library title bar shows no scan, no rating pass and no
model download (rows, the welcome panel and the compact banner show them);
a rating pass shows in its folder's row; the bar keeps only "Quality rating
stopped". States 30, 31, 32 and 37 are relabelled for what they now show.

Owner fixes, round 2: one look for the current item (the Library's
selected row: the blue edge and its soft blue ring) on the current folder
row, the current cluster, the current photo on Focus' rail (the compare set
while comparing) and the active compare pane; keyboard focus on a current
item deepens that ring instead of drawing the focus outline on top of it,
and a current photo that is picked or rejected keeps its mark's edge inside
a blue ring (gallery 809, new). The Folder page drops its hint row (the
shortcut sheet is the keyboard help), and shows no notice when visual
grouping is off by the user's choice (227 is relabelled for that; the
notices for a model still downloading or a failed download stay). Open
Picks is ⌘P. The Library gains a toolbar: a search (⌘F, names and
locations), a filter (all, not reviewed yet, review started, needs
attention) and an order (capture date newest or oldest first, keeping the
month headers; name and recently added, one list). New states 124-130 show
it.

Round 3 (the owner: a new folder must not vanish at the bottom of a long
Library): in the capture-date orders, a folder whose job runs or waits and
that has no capture month yet (a new folder before its first job ends) sits
under "Processing" at the top, above the newest month, and moves to its
month (or to Undated, still last) once the job is over; a rescanned folder
with dates stays in its month. The flat orders (name, recently added) have
no such section. State 117's new folder now has no dates, as Go reports a
new folder, and so sits under Processing; new state 131 shows two new
folders processing at the top of a forty-folder Library.
