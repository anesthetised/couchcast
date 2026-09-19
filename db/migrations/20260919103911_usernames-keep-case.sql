-- Drop index "users_username_idx" from table: "users"
DROP INDEX "public"."users_username_idx";
-- Modify "users" table
ALTER TABLE "public"."users" DROP CONSTRAINT "users_username_unique";
-- Create index "users_username_lower_idx" to table: "users"
CREATE UNIQUE INDEX "users_username_lower_idx" ON "public"."users" ((lower(username)));
