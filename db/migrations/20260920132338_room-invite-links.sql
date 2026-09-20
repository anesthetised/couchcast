-- Create "room_invite_links" table
CREATE TABLE "public"."room_invite_links" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "room_id" uuid NOT NULL,
  "token_hash" bytea NOT NULL,
  "created_by" uuid NULL,
  "expires_at" timestamptz NULL,
  "max_uses" integer NULL,
  "uses" integer NOT NULL DEFAULT 0,
  "revoked_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "room_invite_links_created_by_fkey" FOREIGN KEY ("created_by") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "room_invite_links_room_id_fkey" FOREIGN KEY ("room_id") REFERENCES "public"."rooms" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "room_invite_links_room_idx" to table: "room_invite_links"
CREATE INDEX "room_invite_links_room_idx" ON "public"."room_invite_links" ("room_id");
-- Create index "room_invite_links_token_idx" to table: "room_invite_links"
CREATE UNIQUE INDEX "room_invite_links_token_idx" ON "public"."room_invite_links" ("token_hash");
