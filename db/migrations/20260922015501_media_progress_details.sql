-- Modify "media" table
ALTER TABLE "public"."media" ADD COLUMN "speed_bps" bigint NULL, ADD COLUMN "eta_ms" bigint NULL;
