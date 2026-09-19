package room

import "github.com/anesthetised/couchcast/internal/ingest"

// Aliases keep the room package free of ingest's error wiring while still
// translating its sentinels into user-facing messages.
var (
	errUnsupported = ingest.ErrUnsupportedURL
	errBlocked     = ingest.ErrBlocked
)
