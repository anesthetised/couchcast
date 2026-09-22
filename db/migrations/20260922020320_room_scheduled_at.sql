-- Modify "rooms" table
ALTER TABLE "public"."rooms" ADD COLUMN "scheduled_at" timestamptz NULL;
