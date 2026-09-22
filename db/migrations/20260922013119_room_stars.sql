-- Create "room_stars" table
CREATE TABLE "public"."room_stars" (
  "user_id" uuid NOT NULL,
  "room_id" uuid NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("user_id", "room_id"),
  CONSTRAINT "room_stars_room_id_fkey" FOREIGN KEY ("room_id") REFERENCES "public"."rooms" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "room_stars_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "room_stars_room_id_idx" to table: "room_stars"
CREATE INDEX "room_stars_room_id_idx" ON "public"."room_stars" ("room_id");
