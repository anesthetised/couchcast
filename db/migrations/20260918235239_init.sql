-- Create "audit_log" table
CREATE TABLE "public"."audit_log" (
  "id" bigint NOT NULL GENERATED ALWAYS AS IDENTITY,
  "actor_id" uuid NULL,
  "action" text NOT NULL,
  "target_type" text NOT NULL,
  "target_id" text NOT NULL,
  "room_id" uuid NULL,
  "meta" jsonb NOT NULL DEFAULT '{}',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- Create index "audit_log_actor_id_idx" to table: "audit_log"
CREATE INDEX "audit_log_actor_id_idx" ON "public"."audit_log" ("actor_id");
-- Create index "audit_log_created_at_idx" to table: "audit_log"
CREATE INDEX "audit_log_created_at_idx" ON "public"."audit_log" ("created_at" DESC);
-- Create index "audit_log_room_id_idx" to table: "audit_log"
CREATE INDEX "audit_log_room_id_idx" ON "public"."audit_log" ("room_id");
-- Create "invites" table
CREATE TABLE "public"."invites" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "room_id" uuid NOT NULL,
  "invitee_id" uuid NOT NULL,
  "inviter_id" uuid NULL,
  "status" text NOT NULL DEFAULT 'pending',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "invites_unique" UNIQUE ("room_id", "invitee_id"),
  CONSTRAINT "invites_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'accepted'::text, 'declined'::text]))
);
-- Create index "invites_invitee_pending_idx" to table: "invites"
CREATE INDEX "invites_invitee_pending_idx" ON "public"."invites" ("invitee_id") WHERE (status = 'pending'::text);
-- Create "jobs" table
CREATE TABLE "public"."jobs" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "kind" text NOT NULL,
  "payload" jsonb NOT NULL DEFAULT '{}',
  "status" text NOT NULL DEFAULT 'pending',
  "attempts" integer NOT NULL DEFAULT 0,
  "max_attempts" integer NOT NULL DEFAULT 3,
  "run_at" timestamptz NOT NULL DEFAULT now(),
  "locked_at" timestamptz NULL,
  "locked_by" text NULL,
  "last_error" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "jobs_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'running'::text, 'done'::text, 'failed'::text]))
);
-- Create index "jobs_claim_idx" to table: "jobs"
CREATE INDEX "jobs_claim_idx" ON "public"."jobs" ("run_at", "created_at") WHERE (status = 'pending'::text);
-- Create index "jobs_running_idx" to table: "jobs"
CREATE INDEX "jobs_running_idx" ON "public"."jobs" ("locked_at") WHERE (status = 'running'::text);
-- Create "media" table
CREATE TABLE "public"."media" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "source_key" text NOT NULL,
  "source_url" text NOT NULL,
  "title" text NULL,
  "duration_ms" bigint NULL,
  "thumbnail_url" text NULL,
  "status" text NOT NULL DEFAULT 'queued',
  "progress" real NOT NULL DEFAULT 0,
  "error" text NULL,
  "size_bytes" bigint NULL,
  "renditions" jsonb NOT NULL DEFAULT '[]',
  "s3_prefix" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "last_accessed_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "media_status_check" CHECK (status = ANY (ARRAY['queued'::text, 'probing'::text, 'downloading'::text, 'packaging'::text, 'uploading'::text, 'ready'::text, 'failed'::text]))
);
-- Create index "media_last_accessed_at_idx" to table: "media"
CREATE INDEX "media_last_accessed_at_idx" ON "public"."media" ("last_accessed_at");
-- Create index "media_source_key_idx" to table: "media"
CREATE UNIQUE INDEX "media_source_key_idx" ON "public"."media" ("source_key");
-- Create index "media_status_idx" to table: "media"
CREATE INDEX "media_status_idx" ON "public"."media" ("status");
-- Create "media_blocklist" table
CREATE TABLE "public"."media_blocklist" (
  "source_key" text NOT NULL,
  "reason" text NULL,
  "created_by" uuid NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("source_key")
);
-- Create "media_reports" table
CREATE TABLE "public"."media_reports" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "media_id" uuid NOT NULL,
  "reporter_id" uuid NOT NULL,
  "reason" text NOT NULL,
  "comment" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "resolved_at" timestamptz NULL,
  "resolved_by" uuid NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "media_reports_unique" UNIQUE ("media_id", "reporter_id"),
  CONSTRAINT "media_reports_reason_check" CHECK (reason = ANY (ARRAY['copyright'::text, 'illegal'::text, 'nsfw'::text, 'other'::text]))
);
-- Create index "media_reports_open_idx" to table: "media_reports"
CREATE INDEX "media_reports_open_idx" ON "public"."media_reports" ("media_id") WHERE (resolved_at IS NULL);
-- Create "messages" table
CREATE TABLE "public"."messages" (
  "id" bigint NOT NULL GENERATED ALWAYS AS IDENTITY,
  "room_id" uuid NOT NULL,
  "user_id" uuid NULL,
  "body" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "deleted_at" timestamptz NULL,
  "deleted_by" uuid NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "messages_body_length" CHECK ((char_length(body) >= 1) AND (char_length(body) <= 2000))
);
-- Create index "messages_room_created_idx" to table: "messages"
CREATE INDEX "messages_room_created_idx" ON "public"."messages" ("room_id", "created_at" DESC);
-- Create "queue_items" table
CREATE TABLE "public"."queue_items" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "room_id" uuid NOT NULL,
  "media_id" uuid NOT NULL,
  "added_by" uuid NULL,
  "rank" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- Create index "queue_items_media_id_idx" to table: "queue_items"
CREATE INDEX "queue_items_media_id_idx" ON "public"."queue_items" ("media_id");
-- Create index "queue_items_room_rank_idx" to table: "queue_items"
CREATE INDEX "queue_items_room_rank_idx" ON "public"."queue_items" ("room_id", "rank");
-- Create "queue_votes" table
CREATE TABLE "public"."queue_votes" (
  "item_id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("item_id", "user_id")
);
-- Create "room_bans" table
CREATE TABLE "public"."room_bans" (
  "room_id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "banned_by" uuid NULL,
  "reason" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("room_id", "user_id")
);
-- Create "room_members" table
CREATE TABLE "public"."room_members" (
  "room_id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "role" text NOT NULL DEFAULT 'member',
  "joined_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("room_id", "user_id"),
  CONSTRAINT "room_members_role_check" CHECK (role = ANY (ARRAY['owner'::text, 'moderator'::text, 'member'::text]))
);
-- Create index "room_members_user_id_idx" to table: "room_members"
CREATE INDEX "room_members_user_id_idx" ON "public"."room_members" ("user_id");
-- Create "rooms" table
CREATE TABLE "public"."rooms" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "slug" text NOT NULL,
  "name" text NOT NULL,
  "owner_id" uuid NOT NULL,
  "visibility" text NOT NULL DEFAULT 'public',
  "settings" jsonb NOT NULL DEFAULT '{}',
  "current_item_id" uuid NULL,
  "playing" boolean NOT NULL DEFAULT false,
  "position_ms" bigint NOT NULL DEFAULT 0,
  "position_at" timestamptz NOT NULL DEFAULT now(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "rooms_slug_check" CHECK (slug ~ '^[a-z0-9-]{3,32}$'::text),
  CONSTRAINT "rooms_visibility_check" CHECK (visibility = ANY (ARRAY['public'::text, 'private'::text]))
);
-- Create index "rooms_owner_id_idx" to table: "rooms"
CREATE INDEX "rooms_owner_id_idx" ON "public"."rooms" ("owner_id");
-- Create index "rooms_slug_idx" to table: "rooms"
CREATE UNIQUE INDEX "rooms_slug_idx" ON "public"."rooms" ("slug");
-- Create "sessions" table
CREATE TABLE "public"."sessions" (
  "token_hash" bytea NOT NULL,
  "user_id" uuid NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "last_seen_at" timestamptz NOT NULL DEFAULT now(),
  "expires_at" timestamptz NOT NULL,
  PRIMARY KEY ("token_hash")
);
-- Create index "sessions_expires_at_idx" to table: "sessions"
CREATE INDEX "sessions_expires_at_idx" ON "public"."sessions" ("expires_at");
-- Create index "sessions_user_id_idx" to table: "sessions"
CREATE INDEX "sessions_user_id_idx" ON "public"."sessions" ("user_id");
-- Create "users" table
CREATE TABLE "public"."users" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "username" text NOT NULL,
  "password_hash" text NOT NULL,
  "role" text NOT NULL DEFAULT 'user',
  "banned_at" timestamptz NULL,
  "banned_reason" text NULL,
  "banned_by" uuid NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "users_banned_by_fkey" FOREIGN KEY ("banned_by") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "users_role_check" CHECK (role = ANY (ARRAY['user'::text, 'admin'::text])),
  CONSTRAINT "users_username_unique" CHECK (username = lower(username))
);
-- Create index "users_username_idx" to table: "users"
CREATE UNIQUE INDEX "users_username_idx" ON "public"."users" ("username");
-- Modify "audit_log" table
ALTER TABLE "public"."audit_log" ADD CONSTRAINT "audit_log_actor_id_fkey" FOREIGN KEY ("actor_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL, ADD CONSTRAINT "audit_log_room_id_fkey" FOREIGN KEY ("room_id") REFERENCES "public"."rooms" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;
-- Modify "invites" table
ALTER TABLE "public"."invites" ADD CONSTRAINT "invites_invitee_id_fkey" FOREIGN KEY ("invitee_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE, ADD CONSTRAINT "invites_inviter_id_fkey" FOREIGN KEY ("inviter_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL, ADD CONSTRAINT "invites_room_id_fkey" FOREIGN KEY ("room_id") REFERENCES "public"."rooms" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- Modify "media_blocklist" table
ALTER TABLE "public"."media_blocklist" ADD CONSTRAINT "media_blocklist_created_by_fkey" FOREIGN KEY ("created_by") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;
-- Modify "media_reports" table
ALTER TABLE "public"."media_reports" ADD CONSTRAINT "media_reports_media_id_fkey" FOREIGN KEY ("media_id") REFERENCES "public"."media" ("id") ON UPDATE NO ACTION ON DELETE CASCADE, ADD CONSTRAINT "media_reports_reporter_id_fkey" FOREIGN KEY ("reporter_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE, ADD CONSTRAINT "media_reports_resolved_by_fkey" FOREIGN KEY ("resolved_by") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;
-- Modify "messages" table
ALTER TABLE "public"."messages" ADD CONSTRAINT "messages_deleted_by_fkey" FOREIGN KEY ("deleted_by") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL, ADD CONSTRAINT "messages_room_id_fkey" FOREIGN KEY ("room_id") REFERENCES "public"."rooms" ("id") ON UPDATE NO ACTION ON DELETE CASCADE, ADD CONSTRAINT "messages_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;
-- Modify "queue_items" table
ALTER TABLE "public"."queue_items" ADD CONSTRAINT "queue_items_added_by_fkey" FOREIGN KEY ("added_by") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL, ADD CONSTRAINT "queue_items_media_id_fkey" FOREIGN KEY ("media_id") REFERENCES "public"."media" ("id") ON UPDATE NO ACTION ON DELETE CASCADE, ADD CONSTRAINT "queue_items_room_id_fkey" FOREIGN KEY ("room_id") REFERENCES "public"."rooms" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- Modify "queue_votes" table
ALTER TABLE "public"."queue_votes" ADD CONSTRAINT "queue_votes_item_id_fkey" FOREIGN KEY ("item_id") REFERENCES "public"."queue_items" ("id") ON UPDATE NO ACTION ON DELETE CASCADE, ADD CONSTRAINT "queue_votes_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- Modify "room_bans" table
ALTER TABLE "public"."room_bans" ADD CONSTRAINT "room_bans_banned_by_fkey" FOREIGN KEY ("banned_by") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL, ADD CONSTRAINT "room_bans_room_id_fkey" FOREIGN KEY ("room_id") REFERENCES "public"."rooms" ("id") ON UPDATE NO ACTION ON DELETE CASCADE, ADD CONSTRAINT "room_bans_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- Modify "room_members" table
ALTER TABLE "public"."room_members" ADD CONSTRAINT "room_members_room_id_fkey" FOREIGN KEY ("room_id") REFERENCES "public"."rooms" ("id") ON UPDATE NO ACTION ON DELETE CASCADE, ADD CONSTRAINT "room_members_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- Modify "rooms" table
ALTER TABLE "public"."rooms" ADD CONSTRAINT "rooms_current_item_fk" FOREIGN KEY ("current_item_id") REFERENCES "public"."queue_items" ("id") ON UPDATE NO ACTION ON DELETE SET NULL, ADD CONSTRAINT "rooms_owner_id_fkey" FOREIGN KEY ("owner_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- Modify "sessions" table
ALTER TABLE "public"."sessions" ADD CONSTRAINT "sessions_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
