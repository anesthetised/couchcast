-- Modify "rooms" table
ALTER TABLE "public"."rooms" ADD COLUMN "reminded_for" timestamptz NULL;
-- Create "push_subscriptions" table
CREATE TABLE "public"."push_subscriptions" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "user_id" uuid NOT NULL,
  "endpoint" text NOT NULL,
  "p256dh" text NOT NULL,
  "auth" text NOT NULL,
  "user_agent" text NOT NULL DEFAULT '',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "last_used_at" timestamptz NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "push_subscriptions_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "push_subscriptions_endpoint_idx" to table: "push_subscriptions"
CREATE UNIQUE INDEX "push_subscriptions_endpoint_idx" ON "public"."push_subscriptions" ("endpoint");
-- Create index "push_subscriptions_user_id_idx" to table: "push_subscriptions"
CREATE INDEX "push_subscriptions_user_id_idx" ON "public"."push_subscriptions" ("user_id");
