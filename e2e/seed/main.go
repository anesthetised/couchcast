// Command seed prepares the one playable video the end-to-end tests
// share: a 20 s clip rendered by ffmpeg, packaged like any ingest result,
// uploaded to object storage and registered in the e2e database as a
// ready YouTube media row. `just e2e` runs it inside the e2e-web service,
// whose environment points at the e2e database and the dev bucket; the
// objects are reused across runs.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/anesthetised/couchcast/internal/config"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/mediastore"
	"github.com/anesthetised/couchcast/internal/packager"
)

// The tests queue https://www.youtube.com/watch?v=<VideoID>.
const (
	mediaID    = "0e2e0000-0000-4000-8000-000000000001"
	videoID    = "e2ePlayable"
	durationMs = 20_000
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	err := run(ctx)
	cancel()
	if err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	store, err := mediastore.New(config.S3Config{
		Endpoint:  os.Getenv("COUCHCAST_S3_ENDPOINT"),
		Bucket:    os.Getenv("COUCHCAST_S3_BUCKET"),
		AccessKey: os.Getenv("COUCHCAST_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("COUCHCAST_S3_SECRET_KEY"),
	})
	if err != nil {
		return err
	}
	if err := store.EnsureBucket(ctx); err != nil {
		return err
	}
	prefix := mediastore.Prefix(mediaID)
	if !exists(ctx, store, prefix+packager.ManifestName) {
		if err := render(ctx, store, prefix); err != nil {
			return err
		}
	}

	conn, err := pgx.Connect(ctx, os.Getenv("COUCHCAST_DATABASE_URL"))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	renditions, err := json.Marshal([]entity.Rendition{{ID: "0", Height: 240, Width: 426, Codec: "vp09", Bitrate: 150}})
	if err != nil {
		return err
	}
	_, err = conn.Exec(ctx, `
		INSERT INTO media (id, source_key, source_url, title, duration_ms, status, progress, renditions, s3_prefix)
		VALUES ($1, $2, $3, 'Playable clip', $4, 'ready', 1, $5, $6)
		ON CONFLICT (id) DO NOTHING`,
		uuid.MustParse(mediaID), "youtube:"+videoID, "https://www.youtube.com/watch?v="+videoID, durationMs, renditions, prefix)
	return err
}

func exists(ctx context.Context, store *mediastore.Store, key string) bool {
	obj, err := store.Open(ctx, key)
	if err != nil {
		return false
	}
	defer func() { _ = obj.Close() }()
	_, err = obj.Stat()
	return err == nil
}

// render makes the clip and uploads its DASH package.
func render(ctx context.Context, store *mediastore.Store, prefix string) error {
	dir, err := os.MkdirTemp("", "e2e-seed-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	video, audio := filepath.Join(dir, "v.webm"), filepath.Join(dir, "a.webm")
	secs := fmt.Sprint(durationMs / 1000)
	for _, args := range [][]string{
		{"-f", "lavfi", "-i", "testsrc=duration=" + secs + ":rate=24:size=426x240", "-c:v", "libvpx-vp9", "-g", "24", "-b:v", "150k", "-deadline", "realtime", video},
		{"-f", "lavfi", "-i", "sine=frequency=330:duration=" + secs, "-c:a", "libopus", "-b:a", "48k", audio},
	} {
		cmd := exec.CommandContext(ctx, "ffmpeg", append([]string{"-y", "-hide_banner", "-loglevel", "error"}, args...)...) //nolint:gosec // fixed arguments
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("ffmpeg: %w: %s", err, out)
		}
	}
	out := filepath.Join(dir, "dash")
	if err := os.MkdirAll(out, 0o750); err != nil {
		return err
	}
	p := packager.New("ffmpeg", 2)
	if err := p.Run(ctx, []packager.Input{{Path: video, Width: 426, Height: 240}, {Path: audio, Audio: true}}, out, nil); err != nil {
		return err
	}
	_, err = store.UploadDir(ctx, prefix, out)
	return err
}
