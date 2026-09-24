-- Modify "messages" table
ALTER TABLE "public"."messages" ADD COLUMN "edited_at" timestamptz NULL;
