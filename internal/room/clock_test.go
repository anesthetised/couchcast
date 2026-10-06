package room

import (
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"

	"github.com/anesthetised/couchcast/internal/entity"
)

// The clock is a plain value: these tests need no database.

func TestClockPosition(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	c := clock{rate: 1}

	// Paused, the position stands still.
	c.restart(false, 5_000, t0)
	assert.Equal(t, int64(5_000), c.position(t0.Add(time.Hour), 60_000))

	// Playing, it runs from positionAt at rate, held at the end.
	c.restart(true, 5_000, t0)
	assert.Equal(t, int64(7_000), c.position(t0.Add(2*time.Second), 60_000))
	assert.Equal(t, int64(60_000), c.position(t0.Add(time.Hour), 60_000))
	assert.Equal(t, int64(3_605_000), c.position(t0.Add(time.Hour), 0), "unknown duration: no cap")
	c.rate = 2
	assert.Equal(t, int64(9_000), c.position(t0.Add(2*time.Second), 60_000))
	c.rate = 0.5
	assert.Equal(t, int64(6_000), c.position(t0.Add(2*time.Second), 60_000))
}

func TestClockSetBumpsSeq(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	c := clock{rate: 1, seq: 7}
	c.restart(true, 1_000, t0)
	assert.Equal(t, uint64(8), c.seq)
	pb := c.playback()
	assert.Equal(t, uint64(8), pb.Seq)
	assert.True(t, pb.Playing)
	assert.Equal(t, int64(1_000), pb.PositionMs)
	assert.Equal(t, t0.UnixMilli(), pb.AtServerMs)
}

func TestClockRemaining(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	c := clock{rate: 1}
	c.restart(true, 50_000, t0)
	assert.Equal(t, 10*time.Second, c.remaining(t0, 60_000))
	c.rate = 2
	assert.Equal(t, 5*time.Second, c.remaining(t0, 60_000), "faster playback ends sooner")
	assert.Equal(t, time.Duration(0), c.remaining(t0.Add(time.Hour), 60_000), "ended")
	c.restart(false, 70_000, t0) // a position past a shorter re-probed duration
	assert.Equal(t, time.Duration(0), c.remaining(t0, 60_000), "never negative")
}

func TestClockFrom(t *testing.T) {
	id := uuid.New()
	at := time.Unix(1_800_000_000, 0)
	c := clockFrom(&entity.Room{CurrentItemID: &id, Playing: true, PositionMs: 42, PositionAt: at})
	assert.True(t, c.isCurrent(id))
	assert.False(t, c.isCurrent(uuid.New()))
	assert.Equal(t, 1.0, c.rate, "a room saved before speeds existed plays at 1×")
	assert.Equal(t, entity.PlaybackState{CurrentItemID: &id, Playing: true, PositionMs: 42, PositionAt: at, Rate: 1}, c.state())
	assert.False(t, clock{}.isCurrent(id))
}

func TestStartFromAndClampSeek(t *testing.T) {
	assert.Equal(t, int64(30_000), startFrom(30_000, 60_000))
	assert.Equal(t, int64(0), startFrom(60_000, 60_000), "played to the end: from the top")
	assert.Equal(t, int64(90_000), startFrom(90_000, 0))

	assert.Equal(t, int64(0), clampSeek(-5, 60_000))
	assert.Equal(t, int64(60_000), clampSeek(90_000, 60_000))
	assert.Equal(t, int64(90_000), clampSeek(90_000, 0))
}
