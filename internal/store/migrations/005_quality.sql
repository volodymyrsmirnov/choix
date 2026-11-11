-- Advisory technical-quality rating (see docs/plans/2026-11-11-quality-rating.md).
--
-- quality_raw   the model's raw score (roughly 0..1, higher is better). NULL
--               means "not rated". The 0-10 value shown in the UI is derived
--               from it at read time so the scale can be recalibrated without
--               rescoring.
-- quality_model identifies the scorer (model + crop recipe) that produced the
--               score, or that found the photo unratable (quality_raw NULL
--               with a non-NULL quality_model). A row is pending while this
--               differs from the scorer the binary currently uses.
--
-- Both live on files rather than ai_signals so they survive the analyze
-- upsert and re-clustering untouched.

ALTER TABLE files ADD COLUMN quality_raw REAL;
ALTER TABLE files ADD COLUMN quality_model TEXT;

PRAGMA user_version = 5;
