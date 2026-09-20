-- Create "room_mutes" table
CREATE TABLE "public"."room_mutes" (
  "room_id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "muted_by" uuid NULL,
  "reason" text NOT NULL DEFAULT '',
  "until" timestamptz NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("room_id", "user_id"),
  CONSTRAINT "room_mutes_muted_by_fkey" FOREIGN KEY ("muted_by") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "room_mutes_room_id_fkey" FOREIGN KEY ("room_id") REFERENCES "public"."rooms" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "room_mutes_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
