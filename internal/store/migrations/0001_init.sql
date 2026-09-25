-- Raw battery reading, one row per daemon tick. ts = UTC unix seconds.
CREATE TABLE IF NOT EXISTS samples (
    ts          INTEGER PRIMARY KEY,
    pct         INTEGER NOT NULL,
    on_ac       INTEGER NOT NULL,
    charging    INTEGER NOT NULL,
    watts       REAL,
    raw_cur_mah INTEGER,
    raw_max_mah INTEGER
);

-- Per-app energy impact (unitless, from `top -o power`), top 15 per tick.
CREATE TABLE IF NOT EXISTS app_energy (
    ts        INTEGER NOT NULL,
    app       TEXT    NOT NULL,
    energy    REAL    NOT NULL,
    cpu_pct   REAL,
    is_system INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (ts, app)
);
CREATE INDEX IF NOT EXISTS app_energy_app_ts ON app_energy (app, ts);

-- One row per calendar day, kept forever.
CREATE TABLE IF NOT EXISTS health (
    day         TEXT PRIMARY KEY,
    cycles      INTEGER,
    raw_max_mah INTEGER,
    nominal_mah INTEGER,
    design_mah  INTEGER,
    temp_c      REAL,
    condition   TEXT
);

-- Rollup of samples/app_energy older than 90 days; app_energy is JSON {app: sum}.
CREATE TABLE IF NOT EXISTS daily_rollup (
    day          TEXT PRIMARY KEY,
    min_battery  INTEGER NOT NULL DEFAULT 0,
    min_ac       INTEGER NOT NULL DEFAULT 0,
    min_asleep   INTEGER NOT NULL DEFAULT 0,
    pct_consumed REAL,
    app_energy   TEXT
);
