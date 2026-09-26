// Package hub owns WebSocket connections: it authenticates them, relays
// client commands to the room and fans room broadcasts out to clients.
package hub

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"golang.org/x/time/rate"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/metrics"
	"github.com/anesthetised/couchcast/internal/protocol"
	"github.com/anesthetised/couchcast/internal/room"
)

// ActorResolver rebuilds the actor for a user in a room, so that role
// changes and bans made over REST apply to live connections.
type ActorResolver func(ctx context.Context, rm *entity.Room, user *entity.User) (access.Actor, error)

// Hub creates connections.
type Hub struct {
	manager *room.Manager
	resolve ActorResolver
	logger  *slog.Logger
	metrics *metrics.Metrics

	// ReadTimeout closes connections that stay silent (the client pings
	// every few seconds).
	ReadTimeout time.Duration
}

// New creates a hub.
func New(manager *room.Manager, resolve ActorResolver, logger *slog.Logger, m *metrics.Metrics) *Hub {
	return &Hub{manager: manager, resolve: resolve, logger: logger, metrics: m, ReadTimeout: 90 * time.Second}
}

const (
	sendBuffer   = 256
	readLimit    = 64 << 10
	writeTimeout = 10 * time.Second
)

// Serve upgrades the request and runs the connection until it closes. The
// caller has already verified the actor may view the room.
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, rm *entity.Room, actor access.Actor) {
	ctx := r.Context()

	live, err := h.manager.Get(ctx, rm.ID)
	if err != nil {
		h.logger.ErrorContext(ctx, "load room", "room", rm.Slug, "error", err)
		http.Error(w, "room unavailable", http.StatusServiceUnavailable)
		return
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		h.logger.WarnContext(ctx, "websocket accept", "error", err)
		return
	}
	ws.SetReadLimit(readLimit)

	c := newConn(ws, h, live, rm, actor)
	h.metrics.WSConnectionDelta(1)
	defer h.metrics.WSConnectionDelta(-1)

	c.run(ctx)
}

// conn is one WebSocket client.
type conn struct {
	ws     *websocket.Conn
	hub    *Hub
	room   *room.Room
	entity *entity.Room
	actor  access.Actor

	out     chan any
	limiter *rate.Limiter
	// ref is the Ref of the command being dispatched, echoed in its
	// errors; only the read goroutine touches it.
	ref int64

	// Close only signals: the write loop sends what is still queued (a
	// kicked message), then the close frame with the reason. Closing
	// never blocks the room, which calls it under its lock.
	closeOnce   sync.Once
	closed      chan struct{}
	closeReason string
}

func newConn(ws *websocket.Conn, h *Hub, live *room.Room, rm *entity.Room, actor access.Actor) *conn {
	return &conn{
		ws: ws, hub: h, room: live, entity: rm, actor: actor,
		out: make(chan any, sendBuffer), closed: make(chan struct{}),
		limiter: rate.NewLimiter(20, 40),
	}
}

// Send implements room.Conn. It never blocks: a client that cannot keep
// up is disconnected.
func (c *conn) Send(msg any) {
	select {
	case c.out <- msg:
	case <-c.closed:
	default:
		c.Close("slow client")
	}
}

// Close implements room.Conn.
func (c *conn) Close(reason string) {
	c.closeOnce.Do(func() {
		c.closeReason = reason
		close(c.closed)
	})
}

func (c *conn) run(ctx context.Context) {
	written := make(chan struct{})
	go func() {
		defer close(written)
		c.writeLoop(ctx)
	}()

	c.room.Join(ctx, c, c.actor)
	defer c.room.Leave(c)

	c.readLoop(ctx)
	c.Close("bye")
	<-written
}

func (c *conn) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			_ = c.ws.CloseNow()
			return
		case <-c.closed:
			c.flush(ctx)
			status := websocket.StatusPolicyViolation
			if c.closeReason == room.ReasonShutdown {
				status = websocket.StatusGoingAway // reconnect, the session goes on
			}
			_ = c.ws.Close(status, c.closeReason)
			return
		case msg := <-c.out:
			if !c.write(ctx, msg) {
				c.Close("write failed")
			}
		}
	}
}

// flush writes the messages queued before the close, so a kicked client
// learns why.
func (c *conn) flush(ctx context.Context) {
	for {
		select {
		case msg := <-c.out:
			if !c.write(ctx, msg) {
				return
			}
		default:
			return
		}
	}
}

func (c *conn) write(ctx context.Context, msg any) bool {
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return wsjson.Write(wctx, c.ws, msg) == nil
}

func (c *conn) readLoop(ctx context.Context) {
	for {
		rctx, cancel := context.WithTimeout(ctx, c.hub.ReadTimeout)
		_, data, err := c.ws.Read(rctx)
		cancel()
		if err != nil {
			status := websocket.CloseStatus(err)
			if status == -1 && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				c.hub.logger.DebugContext(ctx, "websocket read", "room", c.entity.Slug, "error", err)
			}
			return
		}

		if !c.limiter.Allow() {
			c.sendError(protocol.CodeRateLimit, "too many commands")
			continue
		}

		c.dispatch(ctx, data)
	}
}

func (c *conn) sendError(code, msg string) {
	c.Send(protocol.Error{Type: protocol.TypeError, Code: code, Message: msg, Ref: c.ref})
}

// freshActor re-resolves membership and bans for mutating commands.
func (c *conn) freshActor(ctx context.Context) (access.Actor, bool) {
	if c.actor.User == nil {
		return c.actor, true
	}
	actor, err := c.hub.resolve(ctx, c.entity, c.actor.User)
	if err != nil {
		c.hub.logger.ErrorContext(ctx, "resolve actor", "error", err)
		c.sendError(protocol.CodeInternal, "internal error")
		return actor, false
	}
	if actor.Banned {
		c.Send(protocol.Kicked{Type: protocol.TypeKicked, Reason: "banned"})
		c.Close("banned")
		return actor, false
	}
	if actor.Role() != c.actor.Role() {
		c.actor = actor
		c.room.UpdateRole(c, actor.Role())
	}
	return actor, true
}

func (c *conn) dispatch(ctx context.Context, data []byte) {
	env, msg, err := protocol.DecodeEnvelope(data)
	c.ref = env.Ref
	defer func() { c.ref = 0 }()
	typ := env.Type
	if err != nil {
		c.sendError(protocol.CodeInvalid, err.Error())
		return
	}

	// Cheap, permission-free messages first.
	switch typ {
	case protocol.TypePing:
		c.Send(protocol.Pong{Type: protocol.TypePong, T0: msg.(*protocol.Ping).T0, T1: time.Now().UnixMilli()})
		return
	case protocol.TypeReport:
		rep := msg.(*protocol.Report)
		c.room.Report(c, rep.State, rep.PositionMs)
		return
	}

	actor, ok := c.freshActor(ctx)
	if !ok {
		return
	}

	cmdCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	switch typ {
	case protocol.TypePlay:
		if msg.(*protocol.Play).Countdown {
			err = c.room.PlayCountdown(cmdCtx, actor)
		} else {
			err = c.room.Play(cmdCtx, actor)
		}
	case protocol.TypePause:
		err = c.room.Pause(cmdCtx, actor)
	case protocol.TypeSeek:
		err = c.room.Seek(cmdCtx, actor, msg.(*protocol.Seek).PositionMs)
	case protocol.TypeNext:
		err = c.room.Next(cmdCtx, actor)
	case protocol.TypeJump:
		err = c.room.Jump(cmdCtx, actor, msg.(*protocol.ItemRef).ItemID)
	case protocol.TypeQueueAdd:
		m := msg.(*protocol.QueueAdd)
		err = c.room.QueueAdd(cmdCtx, actor, m.URL, m.Next, m.Force)
	case protocol.TypeQueueAddMany:
		m := msg.(*protocol.QueueAddMany)
		err = c.room.QueueAddMany(cmdCtx, actor, m.URLs, m.Next)
	case protocol.TypeQueueReplay:
		err = c.room.QueueReplay(cmdCtx, actor, msg.(*protocol.ItemRef).ItemID)
	case protocol.TypeQueueClearPlayed:
		err = c.room.QueueClearPlayed(cmdCtx, actor)
	case protocol.TypeQueueClear:
		err = c.room.QueueClear(cmdCtx, actor)
	case protocol.TypeQueueShuffle:
		err = c.room.QueueShuffle(cmdCtx, actor)
	case protocol.TypeQueueRemove:
		err = c.room.QueueRemove(cmdCtx, actor, msg.(*protocol.ItemRef).ItemID)
	case protocol.TypeQueueMove:
		m := msg.(*protocol.QueueMove)
		err = c.room.QueueMove(cmdCtx, actor, m.ItemID, m.AfterID)
	case protocol.TypeQueueRetry:
		err = c.room.QueueRetry(cmdCtx, actor, msg.(*protocol.ItemRef).ItemID)
	case protocol.TypeQueueVote:
		err = c.room.QueueVote(cmdCtx, actor, msg.(*protocol.ItemRef).ItemID)
	case protocol.TypeSkipVote:
		err = c.room.SkipVote(cmdCtx, actor)
	case protocol.TypeRateSet:
		err = c.room.SetRate(cmdCtx, actor, msg.(*protocol.RateSet).Rate)
	case protocol.TypeSessionEnd:
		err = c.room.EndSession(cmdCtx, actor)
	case protocol.TypeSettingsSet:
		err = c.room.SettingsSet(cmdCtx, actor, *msg.(*protocol.SettingsSet))
	case protocol.TypeChatSend:
		m := msg.(*protocol.ChatSend)
		err = c.room.ChatSend(cmdCtx, actor, m.Body, m.ReplyTo)
	case protocol.TypeChatTyping:
		err = c.room.ChatTyping(actor)
	case protocol.TypeReact:
		err = c.room.React(actor, msg.(*protocol.React).Emoji)
	case protocol.TypeChatClear:
		err = c.room.ChatClear(cmdCtx, actor)
	case protocol.TypeChatEdit:
		m := msg.(*protocol.ChatEdit)
		err = c.room.ChatEdit(cmdCtx, actor, m.ID, m.Body)
	case protocol.TypeChatDelete:
		err = c.room.ChatDelete(cmdCtx, actor, msg.(*protocol.ChatDelete).ID)
	case protocol.TypeChatPin:
		err = c.room.ChatPin(cmdCtx, actor, msg.(*protocol.ChatPin).ID)
	case protocol.TypeChatUnpin:
		err = c.room.ChatUnpin(cmdCtx, actor)
	default:
		c.sendError(protocol.CodeInvalid, "unsupported command: "+typ)
		return
	}

	if err != nil {
		var re *room.Error
		if errors.As(err, &re) {
			c.sendError(re.Code, re.Message)
			return
		}
		c.hub.logger.ErrorContext(ctx, "room command", "type", typ, "room", c.entity.Slug, "error", err)
		c.sendError(protocol.CodeInternal, "internal error")
	}
}
