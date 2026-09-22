-- Create "room_slug_history" table
CREATE TABLE "public"."room_slug_history" (
  "slug" text NOT NULL,
  "room_id" uuid NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("slug"),
  CONSTRAINT "room_slug_history_room_id_fkey" FOREIGN KEY ("room_id") REFERENCES "public"."rooms" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "room_slug_history_room_id_idx" to table: "room_slug_history"
CREATE INDEX "room_slug_history_room_id_idx" ON "public"."room_slug_history" ("room_id");
