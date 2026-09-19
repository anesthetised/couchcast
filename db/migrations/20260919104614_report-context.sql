-- Modify "media_reports" table
ALTER TABLE "public"."media_reports" ADD COLUMN "room_id" uuid NULL, ADD COLUMN "added_by" uuid NULL, ADD
CONSTRAINT "media_reports_added_by_fkey" FOREIGN KEY ("added_by") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL, ADD
CONSTRAINT "media_reports_room_fk" FOREIGN KEY ("room_id") REFERENCES "public"."rooms" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;
