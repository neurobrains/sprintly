// Package realtime broadcasts workspace events to connected clients over
// WebSockets.
//
// Scope is per workspace: a client subscribes to exactly one workspace and
// receives every event published for it. Supabase Realtime already streams raw
// row changes; this hub carries the *semantic* events (task moved by whom,
// presence, typing) that a row diff cannot express.
package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
)

// Event is the envelope every message uses in both directions.
type Event struct {
	Type        string          `json:"type"` // "task.moved", "presence.changed", ...
	WorkspaceID uuid.UUID       `json:"workspace_id"`
	ActorID     uuid.UUID       `json:"actor_id,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	At          time.Time       `json:"at"`
}

type client struct {
	id          uuid.UUID
	userID      uuid.UUID
	workspaceID uuid.UUID
	send        chan Event
}

type Hub struct {
	mu      sync.RWMutex
	rooms   map[uuid.UUID]map[uuid.UUID]*client // workspace -> clientID -> client
	closing bool
}

func NewHub() *Hub {
	return &Hub{rooms: map[uuid.UUID]map[uuid.UUID]*client{}}
}

// Publish fans an event out to every client in the workspace. It never blocks:
// a client whose buffer is full is dropped, and its read loop will notice the
// closed channel and reconnect.
func (h *Hub) Publish(ev Event) {
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, c := range h.rooms[ev.WorkspaceID] {
		select {
		case c.send <- ev:
		default:
			slog.Warn("realtime: dropping slow client", "user_id", c.userID)
		}
	}
}

// PublishTo delivers an event to one user's connections only (notifications).
func (h *Hub) PublishTo(workspaceID, userID uuid.UUID, ev Event) {
	ev.WorkspaceID = workspaceID
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, c := range h.rooms[workspaceID] {
		if c.userID != userID {
			continue
		}
		select {
		case c.send <- ev:
		default:
		}
	}
}

// OnlineUsers lists the distinct users currently connected to a workspace.
func (h *Hub) OnlineUsers(workspaceID uuid.UUID) []uuid.UUID {
	h.mu.RLock()
	defer h.mu.RUnlock()

	seen := map[uuid.UUID]struct{}{}
	out := make([]uuid.UUID, 0, len(h.rooms[workspaceID]))
	for _, c := range h.rooms[workspaceID] {
		if _, dup := seen[c.userID]; dup {
			continue
		}
		seen[c.userID] = struct{}{}
		out = append(out, c.userID)
	}
	return out
}

func (h *Hub) add(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[c.workspaceID] == nil {
		h.rooms[c.workspaceID] = map[uuid.UUID]*client{}
	}
	h.rooms[c.workspaceID][c.id] = c
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[c.workspaceID]
	if room == nil {
		return
	}
	if _, ok := room[c.id]; ok {
		delete(room, c.id)
		close(c.send)
	}
	if len(room) == 0 {
		delete(h.rooms, c.workspaceID)
	}
}

// Serve owns one connection for its lifetime: a writer pump plus a reader that
// handles pings and relays presence/typing signals.
func (h *Hub) Serve(ctx context.Context, conn *websocket.Conn, userID, workspaceID uuid.UUID) {
	c := &client{
		id:          uuid.New(),
		userID:      userID,
		workspaceID: workspaceID,
		send:        make(chan Event, 64),
	}
	h.add(c)
	defer h.remove(c)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	h.Publish(Event{
		Type:        "presence.connected",
		WorkspaceID: workspaceID,
		ActorID:     userID,
		Payload:     json.RawMessage(`{"state":"online"}`),
	})
	defer h.Publish(Event{
		Type:        "presence.disconnected",
		WorkspaceID: workspaceID,
		ActorID:     userID,
		Payload:     json.RawMessage(`{"state":"offline"}`),
	})

	go h.writePump(ctx, cancel, conn, c)
	h.readPump(ctx, conn, c)
}

func (h *Hub) writePump(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, c *client) {
	defer cancel()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case ev, ok := <-c.send:
			if !ok {
				return
			}
			writeCtx, cancelWrite := context.WithTimeout(ctx, 10*time.Second)
			err := wsjson.Write(writeCtx, conn, ev)
			cancelWrite()
			if err != nil {
				return
			}

		case <-ticker.C:
			pingCtx, cancelPing := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pingCtx)
			cancelPing()
			if err != nil {
				return
			}
		}
	}
}

// inbound is what a client may send. Everything else is ignored — clients do not
// get to publish arbitrary event types.
type inbound struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func (h *Hub) readPump(ctx context.Context, conn *websocket.Conn, c *client) {
	conn.SetReadLimit(32 * 1024)
	for {
		var msg inbound
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			return
		}

		switch msg.Type {
		case "presence.update", "typing.start", "typing.stop":
			h.Publish(Event{
				Type:        msg.Type,
				WorkspaceID: c.workspaceID,
				ActorID:     c.userID,
				Payload:     msg.Payload,
			})
		case "ping":
			select {
			case c.send <- Event{Type: "pong", WorkspaceID: c.workspaceID, At: time.Now().UTC()}:
			default:
			}
		}
	}
}

// Marshal is a small helper so handlers can publish without importing encoding/json.
func Marshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
