-- Modify "messages" table
ALTER TABLE "public"."messages" ADD COLUMN "reply_to" bigint NULL, ADD
CONSTRAINT "messages_reply_to_fkey" FOREIGN KEY ("reply_to") REFERENCES "public"."messages" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;
-- Modify "rooms" table
ALTER TABLE "public"."rooms" ADD COLUMN "pinned_message_id" bigint NULL, ADD
CONSTRAINT "rooms_pinned_message_id_fkey" FOREIGN KEY ("pinned_message_id") REFERENCES "public"."messages" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;
