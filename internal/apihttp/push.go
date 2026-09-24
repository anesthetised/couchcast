package apihttp

import (
	"context"
	"net/http"

	"uuid"

	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/notify"
	"github.com/anesthetised/couchcast/internal/webpush"
)

// PushStore keeps browsers' Web Push subscriptions.
type PushStore interface {
	SavePushSubscription(ctx context.Context, userID uuid.UUID, endpoint, p256dh, auth, userAgent string) error
	DeletePushSubscription(ctx context.Context, userID uuid.UUID, endpoint string) error
}

// Notifier sends push notifications (notify.Notifier; nil-safe).
type Notifier interface {
	Notify(user uuid.UUID, msg notify.Message)
}

type pushSubscriptionRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// handlePushKey hands the VAPID public key to the page subscribing.
func (s *Server) handlePushKey(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"publicKey": s.deps.PushPublicKey})
}

// handlePushSubscribe stores the caller's browser subscription. Only
// endpoints on known push services are accepted: the server posts to
// them later, and must never be steered at anything else.
func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	var req pushSubscriptionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	sub := webpush.Subscription{Endpoint: req.Endpoint, P256dh: req.Keys.P256dh, Auth: req.Keys.Auth}
	if len(req.Endpoint) > 1024 || !webpush.AllowedEndpoint(req.Endpoint) {
		writeError(w, http.StatusBadRequest, "unsupported push service")
		return
	}
	if !webpush.ValidSubscription(sub) {
		writeError(w, http.StatusBadRequest, "malformed subscription keys")
		return
	}
	ua := r.UserAgent()
	if len(ua) > 300 {
		ua = ua[:300]
	}
	if err := s.deps.Push.SavePushSubscription(r.Context(), user.ID, sub.Endpoint, sub.P256dh, sub.Auth, ua); err != nil {
		s.internalError(w, r, "save push subscription", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	var req pushSubscriptionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.deps.Push.DeletePushSubscription(r.Context(), user.ID, req.Endpoint); err != nil {
		s.internalError(w, r, "delete push subscription", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// notifyInvite tells the invitee's browsers; the tag matches the one the
// page uses for the same invite.
func (s *Server) notifyInvite(inviteID, invitee uuid.UUID, inviter string, room *entity.Room) {
	if s.deps.Notifier == nil {
		return
	}
	s.deps.Notifier.Notify(invitee, notify.Message{
		Title: "Room invite",
		Body:  inviter + " invited you to " + room.Name,
		URL:   "/",
		Tag:   "invite-" + inviteID.String(),
	})
}
