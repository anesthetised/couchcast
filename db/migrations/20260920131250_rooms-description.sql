-- Modify "rooms" table
ALTER TABLE "public"."rooms" ADD COLUMN "description" text NOT NULL DEFAULT '';
