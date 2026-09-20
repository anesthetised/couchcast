// Package hub owns WebSocket connections: it authenticates them, relays
// client commands to the room and fans room broadcasts out to clients.
package hub

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
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
	closed  chan struct{}
	limiter *rate.Limiter
	cancel  context.CancelFunc
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
	select {
	case <-c.closed:
		return
	default:
		close(c.closed)
	}
	if c.cancel != nil {
		c.cancel()
	}
	_ = c.ws.Close(websocket.StatusPolicyViolation, reason)
}

func (c *conn) run(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	c.cancel = cancel
	defer cancel()

	go c.writeLoop(ctx)

	c.room.Join(ctx, c, c.actor)
	defer c.room.Leave(c)

	c.readLoop(ctx)
	c.Close("bye")
}

func (c *conn) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.closed:
			return
		case msg := <-c.out:
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := wsjson.Write(wctx, c.ws, msg)
			cancel()
			if err != nil {
				c.Close("write failed")
				return
			}
		}
	}
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
	c.Send(protocol.Error{Type: protocol.TypeError, Code: code, Message: msg})
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
	typ, msg, err := protocol.Decode(data)
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
		err = c.room.Play(cmdCtx, actor)
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
		err = c.room.QueueAdd(cmdCtx, actor, m.URL, m.Next)
	case protocol.TypeQueueReplay:
		err = c.room.QueueReplay(cmdCtx, actor, msg.(*protocol.ItemRef).ItemID)
	case protocol.TypeQueueClear:
		err = c.room.QueueClearPlayed(cmdCtx, actor)
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
	case protocol.TypeSessionEnd:
		err = c.room.EndSession(cmdCtx, actor)
	case protocol.TypeSettingsSet:
		err = c.room.SettingsSet(cmdCtx, actor, *msg.(*protocol.SettingsSet))
	case protocol.TypeChatSend:
		err = c.room.ChatSend(cmdCtx, actor, msg.(*protocol.ChatSend).Body)
	case protocol.TypeChatDelete:
		err = c.room.ChatDelete(cmdCtx, actor, msg.(*protocol.ChatDelete).ID)
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
