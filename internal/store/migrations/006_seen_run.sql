-- Walk generation (scanner.Walk).
--
-- seen_run  the generation of the last walk that saw the file on disk. Each
--           walk takes a generation no row holds yet (FilesRepo.NewWalkRun:
--           one more than the largest stored), stamps it on every file it
--           visits (UpsertOnRescan, RecordDiscoveryFailure), and when it has
--           seen the whole folder marks every other row missing in one
--           statement (MarkMissingNotSeen). That replaces the list of seen
--           paths bound as SQL parameters, which failed above SQLite's
--           32 766-parameter limit. NULL: not seen by a walk since this
--           migration (rows written by v1, or by Insert).

ALTER TABLE files ADD COLUMN seen_run INTEGER;

PRAGMA user_version = 6;
