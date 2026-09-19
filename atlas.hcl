// Atlas configuration. Migrations are generated from db/schema.sql and
// applied with `just migrate`; both run inside the atlas container.

env "local" {
  src = "file://db/schema.sql"
  url = getenv("COUCHCAST_DATABASE_URL")
  dev = getenv("ATLAS_DEV_URL")

  migration {
    dir = "file://db/migrations"
  }

  format {
    migrate {
      diff = "{{ sql . \"  \" }}"
    }
  }
}
