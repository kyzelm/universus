-- The schema (02 Architecture/Database Schema.md). Small, boring, sufficient:
-- the expected concurrent user count is in single digits, and every structure
-- here exists because something reads it rather than because a schema usually
-- has one.
CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    email         TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    display_name  TEXT UNIQUE NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    flagged       BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE ratings (
    user_id     BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    lp          INTEGER NOT NULL DEFAULT 0,
    tier        SMALLINT NOT NULL DEFAULT 0,   -- 0=Rookie, see Game Modes
    matches     INTEGER NOT NULL DEFAULT 0,
    wins        INTEGER NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE matches (
    id            BIGSERIAL PRIMARY KEY,
    mode          TEXT NOT NULL,               -- 'ranked' | 'casual'
    p1_id         BIGINT NOT NULL REFERENCES users(id),
    p2_id         BIGINT NOT NULL REFERENCES users(id),
    p1_character  SMALLINT NOT NULL,
    p2_character  SMALLINT NOT NULL,
    rng_seed      BIGINT NOT NULL,
    winner        SMALLINT,                    -- 1, 2, or NULL (unresolved)
    end_frame     INTEGER,
    transport     TEXT,                        -- 'p2p' | 'relay', a thesis statistic
    avg_rtt_ms    INTEGER,
    rollback_avg  REAL,                        -- measurement, not gameplay: float is fine here
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    verified      TEXT NOT NULL DEFAULT 'pending'  -- pending|ok|mismatch|partial
);

-- input_log is BYTEA and not JSON: a match is ~14 KB packed and several times
-- that as JSON, and these are *exactly* the bytes the sim consumes, so there is
-- no parsing step between storage and re-simulation.
CREATE TABLE match_submissions (
    match_id    BIGINT NOT NULL REFERENCES matches(id) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL REFERENCES users(id),
    input_log   BYTEA NOT NULL,                -- the replay log, header and all
    checksums   BYTEA NOT NULL,                -- packed uint32 every 30 frames
    result      JSONB NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (match_id, user_id)
);

CREATE TABLE verification_jobs (
    match_id    BIGINT PRIMARY KEY REFERENCES matches(id) ON DELETE CASCADE,
    status      TEXT NOT NULL DEFAULT 'queued', -- queued|running|done|failed
    attempts    SMALLINT NOT NULL DEFAULT 0,
    result      JSONB,
    queued_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_matches_player ON matches(p1_id, started_at DESC);
CREATE INDEX idx_ratings_lp     ON ratings(lp DESC);
CREATE INDEX idx_jobs_queued    ON verification_jobs(status) WHERE status = 'queued';
