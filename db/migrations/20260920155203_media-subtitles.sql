-- Modify "media" table
ALTER TABLE "public"."media" ADD COLUMN "subtitles" jsonb NOT NULL DEFAULT '[]';
