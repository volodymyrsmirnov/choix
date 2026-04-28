-- Defend against duplicate file rows that differ only in path casing.
-- macOS APFS is case-insensitive but case-preserving by default, so a file
-- visited as `IMG_001.HEIC` on one run and `img_001.heic` on another is the
-- same on-disk file. The UNIQUE constraint on `path` is case-sensitive, so
-- without this index a re-scan that walks the entry with a different
-- casing would insert a second row, surface the same image as a separate
-- cluster member, and confuse the user.
--
-- Steps:
--   1. Collapse any existing duplicate rows (same LOWER(path)) into the
--      lowest-id one. Their thumbs/cluster memberships/ai_signals/picks
--      cascade-delete via FOREIGN KEY ON DELETE CASCADE, which is what we
--      want — the kept row's data stays untouched and the duplicates'
--      derived rows go away. (Picks on a discarded row are rare and the
--      kept row's existing pick wins; it's the price of de-duping.)
--   2. Add the unique expression index so the situation can't recur.

DELETE FROM files
 WHERE id NOT IN (
   SELECT MIN(id) FROM files GROUP BY LOWER(path)
 );

CREATE UNIQUE INDEX IF NOT EXISTS idx_files_path_nocase ON files(LOWER(path));

PRAGMA user_version = 3;
