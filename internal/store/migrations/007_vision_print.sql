-- Vision image feature prints (built-in grouping).
--
-- vision_print     FeaturePrintLen (768) little-endian float32 values, 3,072
--                  bytes, L2-normalised; NULL with an id when Vision could
--                  not read the image or answered a malformed vector
--                  (stamped, so it is not retried).
-- vision_print_id  how the print was made (analyze.PrintID). A row with no
--                  id or another id is pending for the current one.
--
-- clip_embedding is retired with the other legacy columns: never read, never
-- written, never dropped.

ALTER TABLE ai_signals ADD COLUMN vision_print BLOB;
ALTER TABLE ai_signals ADD COLUMN vision_print_id TEXT;

-- A file whose content hash changes loses its print and its stamp, whoever
-- writes the row. An older build (2.0.0, user_version 6) opens this database
-- as it is and knows nothing of the two columns; when it rescans an edited
-- photo it rewrites content_hash only, and without this trigger the print of
-- the old bytes would stay stamped with the current id, so the backfill would
-- never make it again. UpsertOnRescan clears both columns itself as well, for
-- a change of size or mtime under the same (partial) content hash.
CREATE TRIGGER files_content_changed_clears_print
AFTER UPDATE OF content_hash ON files
WHEN OLD.content_hash IS NOT NEW.content_hash
BEGIN
    UPDATE ai_signals SET vision_print = NULL, vision_print_id = NULL
     WHERE file_id = NEW.id;
END;

PRAGMA user_version = 7;
