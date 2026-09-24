-- Modify "sessions" table
ALTER TABLE "public"."sessions" ADD COLUMN "id" uuid NOT NULL DEFAULT gen_random_uuid(), ADD COLUMN "user_agent" text NOT NULL DEFAULT '', ADD CONSTRAINT "sessions_id_key" UNIQUE ("id");
