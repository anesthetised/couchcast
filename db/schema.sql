-- Desired schema for couchcast. This file is the source of truth; Atlas
-- diffs it against the migration directory to produce versioned migrations
-- (`just migrate-diff <name>`). Never edit db/migrations/* by hand.

-- ---------------------------------------------------------------------------
-- Users and sessions
-- ---------------------------------------------------------------------------

CREATE TABLE users (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    username      text        NOT NULL,
    password_hash text        NOT NULL,
    role          text        NOT NULL DEFAULT 'user',
    banned_at     timestamptz,
    banned_reason text,
    banned_by     uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    -- Avatar palette key chosen by the user ('' = derived from the name).
    avatar_color  text        NOT NULL DEFAULT '',

    CONSTRAINT users_role_check CHECK (role IN ('user', 'admin'))
);

-- Usernames keep the case the user typed; uniqueness and lookups ignore it.
CREATE UNIQUE INDEX users_username_lower_idx ON users (lower(username));

CREATE TABLE sessions (
    token_hash   bytea       PRIMARY KEY,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- ---------------------------------------------------------------------------
-- Media, blocklist, reports
-- ---------------------------------------------------------------------------

CREATE TABLE media (
    id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    source_key       text        NOT NULL,
    source_url       text        NOT NULL,
    title            text,
    duration_ms      bigint,
    thumbnail_url    text,
    status           text        NOT NULL DEFAULT 'queued',
    progress         real        NOT NULL DEFAULT 0,
    error            text,
    size_bytes       bigint,
    renditions       jsonb       NOT NULL DEFAULT '[]'::jsonb,
    s3_prefix        text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    last_accessed_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT media_status_check CHECK (
        status IN ('queued', 'probing', 'downloading', 'packaging', 'uploading', 'ready', 'failed')
    )
);

CREATE UNIQUE INDEX media_source_key_idx ON media (source_key);
CREATE INDEX media_status_idx ON media (status);
CREATE INDEX media_last_accessed_at_idx ON media (last_accessed_at);

CREATE TABLE media_blocklist (
    source_key text        PRIMARY KEY,
    reason     text,
    created_by uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE media_reports (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    media_id    uuid        NOT NULL REFERENCES media (id) ON DELETE CASCADE,
    reporter_id uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Where the report was filed from and who had queued the video there,
    -- captured at report time so they survive later queue changes.
    room_id     uuid, -- FK added after rooms is defined below
    added_by    uuid        REFERENCES users (id) ON DELETE SET NULL,
    reason      text        NOT NULL,
    comment     text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    resolved_by uuid        REFERENCES users (id) ON DELETE SET NULL,

    CONSTRAINT media_reports_reason_check CHECK (reason IN ('copyright', 'illegal', 'nsfw', 'other')),
    CONSTRAINT media_reports_unique UNIQUE (media_id, reporter_id)
);

CREATE INDEX media_reports_open_idx ON media_reports (media_id) WHERE resolved_at IS NULL;

-- ---------------------------------------------------------------------------
-- Rooms, membership, bans, invites
-- ---------------------------------------------------------------------------

CREATE TABLE rooms (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    slug            text        NOT NULL,
    name            text        NOT NULL,
    owner_id        uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    visibility      text        NOT NULL DEFAULT 'public',
    description     text        NOT NULL DEFAULT '',
    settings        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    current_item_id uuid,
    playing         boolean     NOT NULL DEFAULT false,
    position_ms     bigint      NOT NULL DEFAULT 0,
    position_at     timestamptz NOT NULL DEFAULT now(),
    -- Playback speed for the whole room (1 = normal).
    rate            real        NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT rooms_slug_check CHECK (slug ~ '^[a-z0-9-]{3,32}$'),
    CONSTRAINT rooms_visibility_check CHECK (visibility IN ('public', 'private'))
);

CREATE UNIQUE INDEX rooms_slug_idx ON rooms (slug);
CREATE INDEX rooms_owner_id_idx ON rooms (owner_id);

ALTER TABLE media_reports
    ADD CONSTRAINT media_reports_room_fk
    FOREIGN KEY (room_id) REFERENCES rooms (id) ON DELETE SET NULL;

CREATE TABLE room_members (
    room_id   uuid        NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    user_id   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role      text        NOT NULL DEFAULT 'member',
    joined_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (room_id, user_id),
    CONSTRAINT room_members_role_check CHECK (role IN ('owner', 'moderator', 'member'))
);

CREATE INDEX room_members_user_id_idx ON room_members (user_id);

CREATE TABLE room_bans (
    room_id    uuid        NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    banned_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    reason     text,
    created_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (room_id, user_id)
);

-- Timed mutes: the user may watch but not chat, vote or add until `until`.
CREATE TABLE room_mutes (
    room_id    uuid        NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    muted_by   uuid        REFERENCES users (id) ON DELETE SET NULL,
    reason     text        NOT NULL DEFAULT '',
    until      timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (room_id, user_id)
);

CREATE TABLE invites (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id    uuid        NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    invitee_id uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    inviter_id uuid        REFERENCES users (id) ON DELETE SET NULL,
    status     text        NOT NULL DEFAULT 'pending',
    created_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT invites_status_check CHECK (status IN ('pending', 'accepted', 'declined')),
    CONSTRAINT invites_unique UNIQUE (room_id, invitee_id)
);

CREATE INDEX invites_invitee_pending_idx ON invites (invitee_id) WHERE status = 'pending';

-- Invite links let anyone with the URL join as a member. Only the token's
-- hash is stored; expiry and use limits are optional.
CREATE TABLE room_invite_links (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id    uuid        NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    token_hash bytea       NOT NULL,
    created_by uuid        REFERENCES users (id) ON DELETE SET NULL,
    expires_at timestamptz,
    max_uses   integer,
    uses       integer     NOT NULL DEFAULT 0,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX room_invite_links_token_idx ON room_invite_links (token_hash);
CREATE INDEX room_invite_links_room_idx ON room_invite_links (room_id);

-- ---------------------------------------------------------------------------
-- Queue, votes, chat
-- ---------------------------------------------------------------------------

CREATE TABLE queue_items (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id    uuid        NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    media_id   uuid        NOT NULL REFERENCES media (id) ON DELETE CASCADE,
    added_by   uuid        REFERENCES users (id) ON DELETE SET NULL,
    rank       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    -- Set when the item finished or was skipped; played items stay for the
    -- room's history and "loop" until purged.
    played_at  timestamptz
);

CREATE INDEX queue_items_room_rank_idx ON queue_items (room_id, rank) WHERE played_at IS NULL;
CREATE INDEX queue_items_room_played_idx ON queue_items (room_id, played_at DESC) WHERE played_at IS NOT NULL;
CREATE INDEX queue_items_media_id_idx ON queue_items (media_id);

ALTER TABLE rooms
    ADD CONSTRAINT rooms_current_item_fk
    FOREIGN KEY (current_item_id) REFERENCES queue_items (id) ON DELETE SET NULL;

CREATE TABLE queue_votes (
    item_id    uuid        NOT NULL REFERENCES queue_items (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (item_id, user_id)
);

CREATE TABLE messages (
    id         bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    room_id    uuid        NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    user_id    uuid        REFERENCES users (id) ON DELETE SET NULL,
    body       text        NOT NULL,
    -- System lines (joined, added a video, skipped) are part of the log but
    -- carry no author.
    system     boolean     NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    deleted_by uuid        REFERENCES users (id) ON DELETE SET NULL,

    CONSTRAINT messages_body_length CHECK (char_length(body) BETWEEN 1 AND 2000)
);

CREATE INDEX messages_room_created_idx ON messages (room_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Background jobs and audit log
-- ---------------------------------------------------------------------------

CREATE TABLE jobs (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    kind         text        NOT NULL,
    payload      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    status       text        NOT NULL DEFAULT 'pending',
    attempts     integer     NOT NULL DEFAULT 0,
    max_attempts integer     NOT NULL DEFAULT 3,
    run_at       timestamptz NOT NULL DEFAULT now(),
    locked_at    timestamptz,
    locked_by    text,
    last_error   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT jobs_status_check CHECK (status IN ('pending', 'running', 'done', 'failed'))
);

CREATE INDEX jobs_claim_idx ON jobs (run_at, created_at) WHERE status = 'pending';
CREATE INDEX jobs_running_idx ON jobs (locked_at) WHERE status = 'running';

CREATE TABLE audit_log (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_id    uuid        REFERENCES users (id) ON DELETE SET NULL,
    action      text        NOT NULL,
    target_type text        NOT NULL,
    target_id   text        NOT NULL,
    room_id     uuid        REFERENCES rooms (id) ON DELETE SET NULL,
    meta        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_created_at_idx ON audit_log (created_at DESC);
CREATE INDEX audit_log_room_id_idx ON audit_log (room_id);
CREATE INDEX audit_log_actor_id_idx ON audit_log (actor_id);
