-- Drop index "queue_items_room_rank_idx" from table: "queue_items"
DROP INDEX "public"."queue_items_room_rank_idx";
-- Modify "queue_items" table
ALTER TABLE "public"."queue_items" ADD COLUMN "played_at" timestamptz NULL;
-- Create index "queue_items_room_rank_idx" to table: "queue_items"
CREATE INDEX "queue_items_room_rank_idx" ON "public"."queue_items" ("room_id", "rank") WHERE (played_at IS NULL);
-- Create index "queue_items_room_played_idx" to table: "queue_items"
CREATE INDEX "queue_items_room_played_idx" ON "public"."queue_items" ("room_id", "played_at" DESC) WHERE (played_at IS NOT NULL);
