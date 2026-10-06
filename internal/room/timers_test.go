package room

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These need no database: the timer rules hold for any Room value.

func TestAfterLockedSkipsClosedRooms(t *testing.T) {
	r := &Room{}
	var ran atomic.Int32
	done := make(chan struct{})
	r.afterLocked(time.Millisecond, func(ctx context.Context) {
		_, ok := ctx.Deadline()
		assert.True(t, ok, "callbacks get a bounded context")
		ran.Add(1)
		close(done)
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the callback never ran")
	}

	// Armed, then the room closes before it fires (the timer itself is
	// not stopped, as when it fired just before closeLocked).
	fired := make(chan struct{})
	r.mu.Lock()
	r.afterLocked(time.Millisecond, func(context.Context) { ran.Add(1) })
	time.AfterFunc(20*time.Millisecond, func() { close(fired) })
	r.closeLocked()
	r.mu.Unlock()
	<-fired
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, int32(1), ran.Load(), "a closed room runs nothing")
}

// TestStopAllCoversEveryTimer arms every timer and checks none fires.
func TestStopAllCoversEveryTimer(t *testing.T) {
	var fired atomic.Int32
	arm := func() *time.Timer { return time.AfterFunc(20*time.Millisecond, func() { fired.Add(1) }) }

	tm := timers{
		advance: arm(), empty: arm(), buffer: arm(), wait: arm(), countdown: arm(), presence: arm(),
		left: map[uuid.UUID]*time.Timer{uuid.New(): arm(), uuid.New(): arm()},
	}
	// A tripwire for new timers: arm them above, and stop them in stopAll.
	require.Equal(t, 7, reflect.TypeFor[timers]().NumField(), "a new timer: add it here and to stopAll")

	tm.stopAll()
	time.Sleep(60 * time.Millisecond)
	assert.Zero(t, fired.Load())
}

func TestStopClearsTheSlot(t *testing.T) {
	var fired atomic.Int32
	slot := time.AfterFunc(20*time.Millisecond, func() { fired.Add(1) })
	stop(&slot)
	assert.Nil(t, slot)
	stop(&slot) // an empty slot is fine
	time.Sleep(40 * time.Millisecond)
	assert.Zero(t, fired.Load())
}
