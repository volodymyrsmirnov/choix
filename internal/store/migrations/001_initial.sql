CREATE TABLE files (
  id            INTEGER PRIMARY KEY,
  path          TEXT NOT NULL UNIQUE,
  size          INTEGER NOT NULL,
  mtime         INTEGER NOT NULL,
  content_hash  TEXT NOT NULL,
  kind          TEXT NOT NULL,
  format        TEXT NOT NULL,
  device_key    TEXT,
  captured_at   INTEGER,
  width         INTEGER,
  height        INTEGER,
  iso           INTEGER,
  aperture      REAL,
  shutter       TEXT,
  focal_length  REAL,
  raw_exif      BLOB,
  scan_status   TEXT NOT NULL DEFAULT 'discovered',
  error         TEXT
);
CREATE INDEX idx_files_device_time ON files(device_key, captured_at);
CREATE INDEX idx_files_status      ON files(scan_status);

CREATE TABLE thumbs (
  file_id   INTEGER NOT NULL,
  tier      TEXT NOT NULL,
  rel_path  TEXT NOT NULL,
  width     INTEGER, height INTEGER,
  PRIMARY KEY (file_id, tier),
  FOREIGN KEY (file_id) REFERENCES files(id) ON DELETE CASCADE
);

CREATE TABLE clusters (
  id              INTEGER PRIMARY KEY,
  device_key      TEXT NOT NULL,
  time_bucket     INTEGER,
  label           TEXT,
  ai_top_file_id  INTEGER,
  cloud_reasoning TEXT
);
CREATE INDEX idx_clusters_device_time ON clusters(device_key, time_bucket);

CREATE TABLE cluster_members (
  cluster_id INTEGER NOT NULL,
  file_id    INTEGER NOT NULL,
  PRIMARY KEY (cluster_id, file_id),
  FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE,
  FOREIGN KEY (file_id)    REFERENCES files(id)    ON DELETE CASCADE
);

CREATE TABLE ai_signals (
  file_id            INTEGER PRIMARY KEY,
  sharpness          REAL,
  face_count         INTEGER,
  faces_eyes_closed  INTEGER,
  exposure_clip_pct  REAL,
  mean_luma          REAL,
  nima_score         REAL,
  phash              BLOB,
  clip_embedding     BLOB,
  computed_at        INTEGER,
  FOREIGN KEY (file_id) REFERENCES files(id) ON DELETE CASCADE
);

CREATE TABLE picks (
  file_id        INTEGER PRIMARY KEY,
  state          TEXT NOT NULL,
  rating         INTEGER,
  picked_at      INTEGER NOT NULL,
  exported_path  TEXT,
  FOREIGN KEY (file_id) REFERENCES files(id) ON DELETE CASCADE
);

CREATE TABLE scan_runs (
  id            INTEGER PRIMARY KEY,
  started_at    INTEGER NOT NULL,
  finished_at   INTEGER,
  status        TEXT NOT NULL,
  files_total   INTEGER, files_done INTEGER,
  ai_total      INTEGER, ai_done INTEGER,
  cancel_token  TEXT
);
