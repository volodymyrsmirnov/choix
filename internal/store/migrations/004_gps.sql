-- Add GPS coordinate columns to files. Both nullable: the EXIF/QuickTime
-- GPS tags are absent on plenty of devices and on-image processing tools
-- often strip them. The pair is read together by the meta parser
-- (signed via the Ref tags) and surfaced in the Focus EXIF panel.

ALTER TABLE files ADD COLUMN gps_lat REAL;
ALTER TABLE files ADD COLUMN gps_lon REAL;

PRAGMA user_version = 4;
