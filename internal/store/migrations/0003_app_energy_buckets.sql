-- ADR-0006: app energy comes from the kernel's coalition counters, in real
-- units, summed into 15-minute buckets. The top-based table from 0001 was
-- never written, so it is replaced rather than migrated.
DROP TABLE IF EXISTS app_energy;

-- One row per app ever seen. An app is a coalition's name (its bundle).
CREATE TABLE apps (
    id        INTEGER PRIMARY KEY,
    name      TEXT    NOT NULL UNIQUE,
    is_system INTEGER NOT NULL DEFAULT 0
);

-- Energy each app used, in nanojoules, per 15-minute bucket. ts is the
-- bucket start: UTC unix seconds, a multiple of 900.
CREATE TABLE app_energy (
    ts     INTEGER NOT NULL,
    app_id INTEGER NOT NULL REFERENCES apps(id),
    cpu_nj INTEGER NOT NULL DEFAULT 0,
    gpu_nj INTEGER NOT NULL DEFAULT 0,
    ane_nj INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (ts, app_id)
) WITHOUT ROWID;
