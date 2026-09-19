-- Runs once when the postgres volume is first created.
-- The main database is created by POSTGRES_DB; these serve tests and Atlas.
CREATE DATABASE couchcast_test;
CREATE DATABASE atlas_dev;
