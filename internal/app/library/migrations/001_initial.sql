-- library.db: the folders in the user's Library, their cached statistics and
-- the persisted job queue. Separate from each folder's own state.db.

CREATE TABLE folders (
  id TEXT PRIMARY KEY,                 -- 16 lowercase hex characters
  path TEXT NOT NULL, name TEXT NOT NULL, bookmark BLOB NOT NULL,
  volume_uuid TEXT NOT NULL DEFAULT '', volume_name TEXT NOT NULL DEFAULT '',
  added_at INTEGER NOT NULL,
  presence TEXT NOT NULL DEFAULT 'present',   -- present | offline | missing | access_lost
  job_pending INTEGER NOT NULL DEFAULT 0,     -- work is queued, running or was interrupted
  job_kind TEXT NOT NULL DEFAULT '',          -- '' | full | backfill
  job_seq INTEGER NOT NULL DEFAULT 0,         -- sequence number of the latest enqueue; orders the queue and identifies a run
  job_error TEXT NOT NULL DEFAULT '',
  photos INTEGER NOT NULL DEFAULT 0, videos INTEGER NOT NULL DEFAULT 0, clusters INTEGER NOT NULL DEFAULT 0,
  picked INTEGER NOT NULL DEFAULT 0, rejected INTEGER NOT NULL DEFAULT 0, failed INTEGER NOT NULL DEFAULT 0,
  first_captured INTEGER, last_captured INTEGER,
  previews INTEGER NOT NULL DEFAULT 0, previews_stamp INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX idx_folders_path ON folders(LOWER(path));
CREATE TABLE kv (k TEXT PRIMARY KEY, v TEXT NOT NULL);
PRAGMA user_version = 1;
