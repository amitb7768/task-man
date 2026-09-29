-- taskman 0001_init — full schema for the Mongo→Postgres migration.
-- Contract: docs/DESIGN_PG_FSM_MIGRATION.md.
-- ids are TEXT: existing rows migrate with their Mongo ObjectID hex; new rows
-- get UUIDs. The API treats ids as opaque strings.
-- NULL encodes Mongo field-absence ($exists semantics — load-bearing for
-- boardVisibilityFilter's "dueDate exists" arm and both partial uniques).
-- '' is only used where Mongo genuinely stored '' (period backlog, priority
-- none, activity string fields).
-- COLLATE "C" on sorted/range-compared text = Mongo's binary string order
-- (uppercase before lowercase; zero-padded period/date ranges exact).

CREATE TABLE teams (
    id         TEXT PRIMARY KEY,
    name       TEXT COLLATE "C" NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT teams_name_unique UNIQUE (name)
);

CREATE TABLE members (
    id                   TEXT PRIMARY KEY,
    name                 TEXT COLLATE "C" NOT NULL,
    email                TEXT COLLATE "C",
    role                 TEXT NOT NULL DEFAULT '', -- job title, distinct from system_role
    password_hash        TEXT,                     -- NULL = login never enabled
    system_role          TEXT NOT NULL DEFAULT '' CHECK (system_role IN ('', 'ADMIN', 'USER')),
    must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
    disabled             BOOLEAN NOT NULL DEFAULT FALSE,
    last_login_at        TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL
);

-- Mongo parity: email unique only among login-enabled members
-- (partialFilterExpression passwordHash $exists).
CREATE UNIQUE INDEX members_email_login_unique
    ON members (lower(email)) WHERE password_hash IS NOT NULL;

-- Replaces Member.teamIds. CASCADE both ways: deleting a team detaches its
-- members (Mongo left dangling teamIds here — accepted fix, decision #3).
CREATE TABLE member_teams (
    member_id TEXT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    team_id   TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    PRIMARY KEY (member_id, team_id)
);
CREATE INDEX member_teams_team ON member_teams (team_id);

CREATE TABLE tasks (
    id           TEXT PRIMARY KEY,
    title        TEXT COLLATE "C" NOT NULL,
    notes        TEXT,
    horizon      TEXT NOT NULL CHECK (horizon IN ('daily', 'weekly', 'monthly')),
    period       TEXT COLLATE "C" NOT NULL DEFAULT '', -- '' = backlog pool (v7)
    due_date     TEXT COLLATE "C",           -- YYYY-MM-DD; NULL = none (never '')
    status       TEXT NOT NULL CHECK (status IN ('todo', 'in_progress', 'done', 'cancelled')),
    priority     TEXT NOT NULL DEFAULT '' CHECK (priority IN ('', 'low', 'medium', 'high')),
    -- RESTRICT: DeleteTaskCascade deletes children first, inside one tx.
    parent_id    TEXT REFERENCES tasks(id) ON DELETE RESTRICT,
    -- RESTRICT: DeleteTeam's "team has tasks" 409, enforced by the DB.
    team_id      TEXT REFERENCES teams(id) ON DELETE RESTRICT,
    -- SET NULL: DeleteMember's $unset assigneeId, enforced by the DB.
    assignee_id  TEXT REFERENCES members(id) ON DELETE SET NULL,
    -- RESTRICT should never fire: owners are login-enabled members, and
    -- login-enabled members are never hard-deleted (AUTH decision #8).
    owner_id     TEXT REFERENCES members(id) ON DELETE RESTRICT,
    week_of      TEXT COLLATE "C",  -- YYYY-Www; system-managed, present iff team_id set
    recurrence   JSONB, -- {freq, interval?, anchor?, weekdays?, dayOfMonth?}
    series_id    TEXT,  -- correlation id, deliberately NOT an FK (series head may be gone)
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ -- set only on transition into done, never for cancelled
);

-- Mongo parity indexes (EnsureIndexes, verbatim set)
CREATE INDEX tasks_horizon_period   ON tasks (horizon, period);
CREATE INDEX tasks_due_date         ON tasks (due_date);
CREATE INDEX tasks_parent           ON tasks (parent_id);
CREATE INDEX tasks_team_assignee    ON tasks (team_id, assignee_id);
CREATE INDEX tasks_team_week_status ON tasks (team_id, week_of, status);
CREATE INDEX tasks_owner            ON tasks (owner_id);
-- Materialization idempotency: unique partial (seriesId, period), verbatim.
CREATE UNIQUE INDEX tasks_series_period_unique
    ON tasks (series_id, period) WHERE series_id IS NOT NULL;
-- Arch-review addition (missing in Mongo)
CREATE INDEX tasks_team_status_due ON tasks (team_id, status, due_date);
-- $text index intentionally dropped: search is ILIKE at this scale (delta #2).

-- Task.Activity embedded array → child table. Doubles as the FSM WriteAudit
-- sink (one log, not two — no state_history table).
CREATE TABLE task_activity (
    id          TEXT PRIMARY KEY,
    task_id     TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    -- Append order. Mongo's array order is insertion order and `at` can tie,
    -- so ordering is by seq, never by at.
    seq         BIGINT GENERATED ALWAYS AS IDENTITY,
    kind        TEXT NOT NULL CHECK (kind IN ('note', 'status')),
    date        TEXT COLLATE "C" NOT NULL, -- YYYY-MM-DD day bucket; backdating intended
    at          TIMESTAMPTZ NOT NULL,
    by_id       TEXT REFERENCES members(id) ON DELETE SET NULL,
    by_name     TEXT NOT NULL DEFAULT '', -- denormalized at write; reads never join
    text        TEXT NOT NULL DEFAULT '',
    from_status TEXT NOT NULL DEFAULT '',
    to_status   TEXT NOT NULL DEFAULT '',
    edited_at   TIMESTAMPTZ
);
CREATE INDEX task_activity_task ON task_activity (task_id, seq);

CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    token      TEXT NOT NULL,
    user_id    TEXT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT sessions_token_unique UNIQUE (token)
);
-- TTL-index replacement: expiry stays enforced in code; expired rows are
-- swept opportunistically on login.
CREATE INDEX sessions_expires ON sessions (expires_at);
