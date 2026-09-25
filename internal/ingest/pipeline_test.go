package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/jobs"
	"github.com/anesthetised/couchcast/internal/mediastore/storetest"
	"github.com/anesthetised/couchcast/internal/packager"
	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/repository/repotest"
	"github.com/anesthetised/couchcast/internal/source"
)

// fakeSource is an extractor whose "downloads" are short clips rendered
// by ffmpeg, so the rest of the pipeline runs for real.
type fakeSource struct {
	ffmpeg    string
	probe     *source.Probe
	probeErr  error
	failDL    error
	mu        sync.Mutex
	downloads int
}

func (f *fakeSource) Key(url string) (string, bool) {
	if !strings.HasPrefix(url, "https://video.test/") {
		return "", false
	}
	return "test:" + strings.TrimPrefix(url, "https://video.test/"), true
}

func (f *fakeSource) Probe(context.Context, string) (*source.Probe, error) {
	return f.probe, f.probeErr
}

func (f *fakeSource) PlaylistURL(url string) (string, bool, bool) {
	list, ok := strings.CutPrefix(url, "https://video.test/list/")
	return "https://video.test/list/" + list, false, ok
}

func (f *fakeSource) Playlist(_ context.Context, url string, limit int) (*source.Playlist, error) {
	return &source.Playlist{Title: url, Total: limit}, nil
}

func (f *fakeSource) Download(ctx context.Context, _ string, formats []source.Format, dir string, progress func(float64)) (map[string]string, error) {
	f.mu.Lock()
	f.downloads++
	f.mu.Unlock()
	if f.failDL != nil {
		return nil, f.failDL
	}
	out := map[string]string{}
	for i, fm := range formats {
		p := filepath.Join(dir, fm.ID+".webm")
		var args []string
		if fm.IsVideo() {
			args = []string{"-f", "lavfi", "-i", "testsrc=duration=3:rate=10:size=" + strconv.Itoa(fm.Width) + "x" + strconv.Itoa(fm.Height),
				"-c:v", "libvpx-vp9", "-g", "10", "-b:v", "100k", "-deadline", "realtime"}
		} else {
			args = []string{"-f", "lavfi", "-i", "sine=frequency=440:duration=3", "-c:a", "libopus"}
		}
		cmd := exec.CommandContext(ctx, f.ffmpeg, append(append([]string{"-y", "-hide_banner", "-loglevel", "error"}, args...), p)...) //nolint:gosec // test fixture
		if b, err := cmd.CombinedOutput(); err != nil {
			return nil, errors.New(string(b))
		}
		out[fm.ID] = p
		progress(float64(i+1) / float64(len(formats)))
	}
	return out, nil
}

func (f *fakeSource) DownloadSubtitles(_ context.Context, _ string, subs []source.Subtitle, dir string) (map[string]string, error) {
	out := map[string]string{}
	for _, s := range subs {
		if s.Lang == "xx" {
			continue // a track the site no longer serves
		}
		p := filepath.Join(dir, s.Lang+".vtt")
		if err := os.WriteFile(p, []byte("WEBVTT\n\n00:00.000 --> 00:01.000\nhello\n"), 0o600); err != nil {
			return nil, err
		}
		out[s.Lang] = p
	}
	return out, nil
}

func testProbe(thumb string) *source.Probe {
	return &source.Probe{
		Title: "Test clip", DurationMs: 3000, ThumbnailURL: thumb,
		Formats: []source.Format{
			{ID: "v480", Ext: "webm", VCodec: "vp09.00.30.08", Width: 854, Height: 480, Filesize: 1000},
			{ID: "v360", Ext: "webm", VCodec: "vp09.00.21.08", Width: 640, Height: 360, Filesize: 800},
			{ID: "a", Ext: "webm", ACodec: "opus", Filesize: 200},
		},
		Subtitles: []source.Subtitle{{Lang: "en", Name: "English"}, {Lang: "xx", Name: "Gone"}},
		Chapters:  []source.Chapter{{StartMs: 0, EndMs: 1500, Title: "Intro"}},
	}
}

// pipeline wires the real repository, queue, packager and store around
// the fake source.
type pipeline struct {
	repo   *repository.Repo
	queue  *jobs.Queue
	src    *fakeSource
	svc    *Service
	worker *Worker
	list   func(string) []string
}

func newPipeline(t *testing.T) *pipeline {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	store, list := storetest.New(t)
	pool := repotest.Pool(t)
	repo := repository.New(pool)
	queue := jobs.New(pool)

	thumbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var buf bytes.Buffer
		_ = jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 16, 9)), nil)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(buf.Bytes())
	}))
	t.Cleanup(thumbSrv.Close)

	src := &fakeSource{ffmpeg: ffmpeg, probe: testProbe(thumbSrv.URL + "/poster.jpg")}
	logger := slog.New(slog.DiscardHandler)
	return &pipeline{
		repo: repo, queue: queue, src: src, list: list,
		svc:    NewService(repo, queue, src),
		worker: NewWorker(repo, queue, src, packager.New(ffmpeg, 1), store, t.TempDir(), []int{1080, 720, 480, 360}, logger, nil),
	}
}

// run claims the next ingest job and hands it to the worker.
func (p *pipeline) run(t *testing.T) error {
	t.Helper()
	ctx := context.Background()
	job, err := p.queue.Claim(ctx, "test", []string{JobKind})
	require.NoError(t, err)
	require.NotNil(t, job, "a job is waiting")
	return p.worker.Handle(ctx, job)
}

func TestPipelinePackagesAndUploads(t *testing.T) {
	p := newPipeline(t)
	ctx := context.Background()

	m, err := p.svc.EnsureMedia(ctx, p.repo.Pool(), "https://video.test/clip")
	require.NoError(t, err)
	again, err := p.svc.EnsureMedia(ctx, p.repo.Pool(), "https://video.test/clip")
	require.NoError(t, err)
	assert.Equal(t, m.ID, again.ID, "one media row and one job per source")

	require.NoError(t, p.run(t))
	_, err = p.queue.Claim(ctx, "test", []string{JobKind})
	require.ErrorIs(t, err, jobs.ErrNoJobs, "the second add did not enqueue")

	got, err := p.repo.GetMedia(ctx, m.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.MediaReady, got.Status)
	assert.Equal(t, "Test clip", got.Title)
	assert.EqualValues(t, 3000, got.DurationMs)
	require.Len(t, got.Renditions, 2)
	assert.Equal(t, 480, got.Renditions[0].Height)
	assert.Equal(t, "vp09", got.Renditions[0].Codec)
	assert.Equal(t, []entity.Subtitle{{Lang: "en", Name: "English"}}, got.Subtitles, "missing tracks are left out")
	assert.Equal(t, []entity.Chapter{{StartMs: 0, EndMs: 1500, Title: "Intro"}}, got.Chapters)
	assert.Equal(t, "/media/"+m.ID.String()+"/thumb.jpg", got.ThumbnailURL, "the poster is copied, not linked")
	require.NotNil(t, got.Storyboard)
	assert.Positive(t, got.SizeBytes)
	assert.InDelta(t, 1, got.Progress, 0.001)

	keys := p.list("media/" + m.ID.String() + "/")
	names := make([]string, 0, len(keys))
	for _, k := range keys {
		names = append(names, strings.TrimPrefix(k, "media/"+m.ID.String()+"/"))
	}
	assert.Contains(t, names, "manifest.mpd")
	assert.Contains(t, names, "init-0.webm")
	assert.Contains(t, names, "init-2.webm", "audio is the third representation")
	assert.Contains(t, names, "sub-en.vtt")
	assert.Contains(t, names, "thumb.jpg")
	assert.Contains(t, names, "sb-0.jpg")

	// A ready media is not processed again.
	require.NoError(t, p.worker.Handle(ctx, &jobs.Job{Payload: mustJSON(t, Payload{MediaID: m.ID})}))
	assert.Equal(t, 1, p.src.downloads)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func TestPipelineFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("probe errors are retried", func(t *testing.T) {
		p := newPipeline(t)
		p.src.probeErr = errors.New("rate limited")
		m, err := p.svc.EnsureMedia(ctx, p.repo.Pool(), "https://video.test/a")
		require.NoError(t, err)
		err = p.run(t)
		require.Error(t, err)
		assert.False(t, jobs.IsPermanent(err))
		got, _ := p.repo.GetMedia(ctx, m.ID)
		assert.Equal(t, entity.MediaFailed, got.Status)
		assert.Contains(t, got.Error, "rate limited")
	})

	t.Run("no usable format is permanent", func(t *testing.T) {
		p := newPipeline(t)
		p.src.probe.Formats = p.src.probe.Formats[2:] // audio only
		_, err := p.svc.EnsureMedia(ctx, p.repo.Pool(), "https://video.test/b")
		require.NoError(t, err)
		err = p.run(t)
		require.Error(t, err)
		assert.True(t, jobs.IsPermanent(err))
	})

	t.Run("download errors fail the media", func(t *testing.T) {
		p := newPipeline(t)
		p.src.failDL = errors.New("403 from the site")
		m, err := p.svc.EnsureMedia(ctx, p.repo.Pool(), "https://video.test/c")
		require.NoError(t, err)
		require.Error(t, p.run(t))
		got, _ := p.repo.GetMedia(ctx, m.ID)
		assert.Equal(t, entity.MediaFailed, got.Status)

		// Retry puts it back in the queue, and it succeeds this time.
		require.NoError(t, p.svc.Retry(ctx, got))
		p.src.failDL = nil
		require.NoError(t, p.run(t))
		got, _ = p.repo.GetMedia(ctx, m.ID)
		assert.Equal(t, entity.MediaReady, got.Status)
		assert.Error(t, p.svc.Retry(ctx, got), "only failed media is retried")
	})

	t.Run("unreadable jobs are dropped", func(t *testing.T) {
		p := newPipeline(t)
		err := p.worker.Handle(ctx, &jobs.Job{Payload: []byte("{")})
		assert.True(t, jobs.IsPermanent(err))
		err = p.worker.Handle(ctx, &jobs.Job{Payload: mustJSON(t, Payload{MediaID: uuid.New()})})
		assert.True(t, jobs.IsPermanent(err))
	})
}

func TestServiceAdmission(t *testing.T) {
	p := newPipeline(t)
	ctx := context.Background()

	_, err := p.svc.EnsureMedia(ctx, p.repo.Pool(), "https://elsewhere.test/x")
	require.ErrorIs(t, err, ErrUnsupportedURL)

	require.NoError(t, p.repo.BlockSource(ctx, "test:banned", "no", nil))
	_, err = p.svc.EnsureMedia(ctx, p.repo.Pool(), "https://video.test/banned")
	require.ErrorIs(t, err, ErrBlocked)
	_, err = p.svc.Preview(ctx, "https://video.test/banned")
	require.ErrorIs(t, err, ErrBlocked)

	// A new link is probed; a known one answers from the database.
	pv, err := p.svc.Preview(ctx, "https://video.test/new")
	require.NoError(t, err)
	assert.Equal(t, "Test clip", pv.Title)
	assert.Empty(t, pv.Status)
	m, err := p.svc.EnsureMedia(ctx, p.repo.Pool(), "https://video.test/known")
	require.NoError(t, err)
	require.NoError(t, p.repo.SetMediaProbed(ctx, m.ID, "Stored title", 5000, "", nil))
	p.src.probeErr = errors.New("must not probe")
	pv, err = p.svc.Preview(ctx, "https://video.test/known")
	require.NoError(t, err)
	assert.Equal(t, "Stored title", pv.Title)
	assert.Equal(t, entity.MediaQueued, pv.Status)

	list, video, ok := p.svc.PlaylistURL("https://video.test/list/abc")
	assert.True(t, ok)
	assert.False(t, video)
	pl, err := p.svc.Playlist(ctx, list)
	require.NoError(t, err)
	assert.Equal(t, PlaylistLimit, pl.Total)
	_, err = p.svc.Playlist(ctx, "https://video.test/x")
	require.ErrorIs(t, err, ErrUnsupportedURL)
}
