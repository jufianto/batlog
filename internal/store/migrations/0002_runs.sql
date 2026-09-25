-- One row per recorder start. A gap between samples that contains a start
-- is a data gap (daemon down, Mac off); any other gap is sleep (F3).
CREATE TABLE IF NOT EXISTS runs (
    started INTEGER PRIMARY KEY
);
