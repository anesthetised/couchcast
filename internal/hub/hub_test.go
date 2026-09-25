package hub

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/anesthetised/couchcast/internal/access"
)

func TestSlowClientIsDisconnected(t *testing.T) {
	c := newConn(nil, nil, nil, nil, access.Actor{})
	for range sendBuffer {
		c.Send("frame")
	}
	select {
	case <-c.closed:
		t.Fatal("closed before the buffer filled")
	default:
	}

	// One more than the buffer holds: the client cannot keep up.
	c.Send("frame")
	<-c.closed
	assert.Equal(t, "slow client", c.closeReason)

	// Closing again keeps the first reason; sends after the close are dropped.
	c.Close("bye")
	assert.Equal(t, "slow client", c.closeReason)
	c.Send("late")
}
