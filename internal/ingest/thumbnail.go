package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Thumbnails are copied next to the DASH output so viewers never fetch
// them from the source site (which leaks their address and lets the link
// rot). They are served without a media token.
const (
	thumbnailMaxBytes = 8 << 20
	thumbnailTimeout  = 15 * time.Second
)

// thumbnailExt maps the types sites actually serve to a file extension.
var thumbnailExt = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// ThumbnailFile is the object name of the thumbnail inside the media
// prefix for the given content type; "" when the type is not an image
// we keep.
func ThumbnailFile(contentType string) string {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if ext, ok := thumbnailExt[ct]; ok {
		return "thumb" + ext
	}
	return ""
}

// fetchThumbnail downloads the poster into dir and returns its object
// name. Errors are for the log: a missing thumbnail never fails a job.
func fetchThumbnail(ctx context.Context, client *http.Client, rawURL, dir string) (string, error) {
	if rawURL == "" {
		return "", errors.New("no thumbnail")
	}
	ctx, cancel := context.WithTimeout(ctx, thumbnailTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("thumbnail: status %d", resp.StatusCode)
	}
	name := ThumbnailFile(resp.Header.Get("Content-Type"))
	if name == "" {
		return "", fmt.Errorf("thumbnail: unsupported type %q", resp.Header.Get("Content-Type"))
	}
	f, err := os.Create(filepath.Join(dir, name)) //nolint:gosec // name comes from our own table
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, thumbnailMaxBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if n > thumbnailMaxBytes {
		_ = os.Remove(filepath.Join(dir, name))
		return "", errors.New("thumbnail: too large")
	}
	return name, nil
}
