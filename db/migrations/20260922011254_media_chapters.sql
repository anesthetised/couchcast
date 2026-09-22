-- Modify "media" table
ALTER TABLE "public"."media" ADD COLUMN "chapters" jsonb NOT NULL DEFAULT '[]';
