-- Create "bug_reports" table
CREATE TABLE "public"."bug_reports" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "user_id" uuid NULL,
  "room_id" uuid NULL,
  "media_id" uuid NULL,
  "category" text NOT NULL,
  "description" text NOT NULL DEFAULT '',
  "client" jsonb NOT NULL DEFAULT '{}',
  "server" jsonb NOT NULL DEFAULT '{}',
  "frame" bytea NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "resolved_at" timestamptz NULL,
  "resolved_by" uuid NULL,
  "note" text NOT NULL DEFAULT '',
  PRIMARY KEY ("id"),
  CONSTRAINT "bug_reports_media_id_fkey" FOREIGN KEY ("media_id") REFERENCES "public"."media" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "bug_reports_resolved_by_fkey" FOREIGN KEY ("resolved_by") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "bug_reports_room_fk" FOREIGN KEY ("room_id") REFERENCES "public"."rooms" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "bug_reports_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "bug_reports_category_check" CHECK (category = ANY (ARRAY['playback'::text, 'sync'::text, 'subtitles'::text, 'chat'::text, 'other'::text])),
  CONSTRAINT "bug_reports_description_length" CHECK (char_length(description) <= 2000)
);
-- Create index "bug_reports_created_at_idx" to table: "bug_reports"
CREATE INDEX "bug_reports_created_at_idx" ON "public"."bug_reports" ("created_at" DESC);
-- Create index "bug_reports_open_idx" to table: "bug_reports"
CREATE INDEX "bug_reports_open_idx" ON "public"."bug_reports" ("created_at" DESC) WHERE (resolved_at IS NULL);
