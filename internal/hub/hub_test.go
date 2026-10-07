package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/room"
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

// closedWith runs a connection's write loop over a real socket, closes it
// with reason (after a queued message, like a kick) and returns what the
// client saw.
func closedWith(t *testing.T, reason string) (websocket.StatusCode, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c := newConn(ws, nil, nil, nil, access.Actor{})
		c.Send("last words")
		c.Close(reason)
		c.writeLoop(r.Context())
	}))
	defer srv.Close()

	ws, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	_, data, err := ws.Read(ctx)
	require.NoError(t, err)
	assert.JSONEq(t, `"last words"`, string(data), "queued messages go out before the close")
	_, _, err = ws.Read(ctx)
	var ce websocket.CloseError
	require.ErrorAs(t, err, &ce)
	return ce.Code, ce.Reason
}

func TestCloseStatus(t *testing.T) {
	// Transport trouble: the browser reconnects (anything but 1008).
	for _, reason := range []string{reasonSlowClient, reasonWriteFailed, reasonBye} {
		code, got := closedWith(t, reason)
		assert.Equal(t, websocket.StatusTryAgainLater, code, reason)
		assert.Equal(t, reason, got)
	}

	code, _ := closedWith(t, room.ReasonShutdown)
	assert.Equal(t, websocket.StatusGoingAway, code)

	// Decisions about the viewer end the session even if the kicked
	// message was lost.
	for _, reason := range []string{"banned", "removed from room", "room deleted", "left", "session ended"} {
		code, got := closedWith(t, reason)
		assert.Equal(t, websocket.StatusPolicyViolation, code, reason)
		assert.Equal(t, reason, got)
	}
}
