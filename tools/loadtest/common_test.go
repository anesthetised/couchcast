package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTypeOf(t *testing.T) {
	assert.Equal(t, "room.state", typeOf([]byte(`{"type":"room.state","queue":[]}`)))
	assert.Equal(t, "playback", typeOf([]byte(`{ "positionMs": 1, "type": "playback" }`)), "falls back to decoding")
	assert.Empty(t, typeOf([]byte(`not json`)))
}

func TestQueueOf(t *testing.T) {
	q, ok := queueOf("room.state", []byte(`{"type":"room.state","queue":[{"id":"a","current":true},{"id":"b","voted":true,"votes":3}]}`))
	require.True(t, ok)
	assert.Equal(t, []entry{{ID: "a", Current: true}, {ID: "b", Voted: true}}, q)

	q, ok = queueOf("welcome", []byte(`{"type":"welcome","snapshot":{"queue":[{"id":"c"}]}}`))
	require.True(t, ok)
	assert.Equal(t, []entry{{ID: "c"}}, q)

	_, ok = queueOf("playback", []byte(`{"type":"playback"}`))
	assert.False(t, ok)
}

func TestSeeksMatchPlaybackByPosition(t *testing.T) {
	s := newSeeks()
	s.sent(10_037)
	s.seen("playback", []byte(`{"type":"playback","positionMs":10037}`))
	s.seen("playback", []byte(`{"type":"playback","positionMs":5}`)) // not a timed seek
	s.seen("room.state", []byte(`{"type":"room.state"}`))
	assert.Equal(t, 1, s.lat.len())
}

func TestLatencies(t *testing.T) {
	var l latencies
	assert.Equal(t, "n 0, p50 - p95 - p99 - max -", l.String())
	for i := 1; i <= 100; i++ {
		l.add(time.Duration(i) * time.Millisecond)
	}
	assert.Equal(t, "n 100, p50 51ms p95 96ms p99 100ms max 100ms", l.String())
}
