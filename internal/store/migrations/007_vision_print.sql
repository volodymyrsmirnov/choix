-- Vision image feature prints (built-in grouping).
--
-- vision_print     FeaturePrintLen (768) little-endian float32 values, 3,072
--                  bytes, L2-normalised; NULL when Vision could not make one
--                  for a readable preview (stamped, so it is not retried).
-- vision_print_id  how the print was made (analyze.PrintID). A row with no
--                  id or another id is pending for the current one.
--
-- clip_embedding is retired with the other legacy columns: never read, never
-- written, never dropped.

ALTER TABLE ai_signals ADD COLUMN vision_print BLOB;
ALTER TABLE ai_signals ADD COLUMN vision_print_id TEXT;

PRAGMA user_version = 7;
