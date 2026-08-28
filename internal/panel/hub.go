package panel

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"xuanwu/internal/wire"
)

// Hub tracks live agent connections keyed by node id.
type Hub struct {
	mu    sync.RWMutex
	conns map[int64]*nodeConn
	app   *App
}

type nodeConn struct {
	nodeID int64
	ws     *websocket.Conn
	send   chan wire.Msg
	once   sync.Once
}

func NewHub(app *App) *Hub {
	return &Hub{conns: map[int64]*nodeConn{}, app: app}
}

var upgrader = websocket.Upgrader{
	CheckOrigin:     checkWSOrigin,
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
}

// checkWSOrigin permits the node agent — a non-browser client that sends no
// Origin header — and rejects any cross-origin browser request. Node access is
// still gated by the registration token; pinning the origin additionally stops a
// malicious web page from driving the node socket from a logged-in operator's
// browser (the panel's own SPA never uses this endpoint).
func checkWSOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

// handleWS is the HTTP handler for GET /api/node/ws.
func (h *Hub) handleWS(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadDeadline(time.Now().Add(15 * time.Second))
	var reg wire.Msg
	if err := ws.ReadJSON(&reg); err != nil || reg.Type != wire.TypeRegister {
		ws.Close()
		return
	}
	node, err := h.app.store.GetNodeByToken(reg.Token)
	if err != nil {
		log.Printf("node ws: bad token")
		ws.Close()
		return
	}
	ws.SetReadDeadline(time.Time{})

	c := &nodeConn{nodeID: node.ID, ws: ws, send: make(chan wire.Msg, 8)}
	h.register(c)
	log.Printf("node %d (%s) connected v=%s", node.ID, node.Name, reg.Version)
	_ = h.app.store.TouchNode(node.ID)
	_ = h.app.store.SetNodeAgentVersion(node.ID, reg.Version)
	h.app.noticeOnline(node.ID, node.Name)

	go c.writeLoop()
	h.app.forceSyncNode(node.ID)

	h.readLoop(c)
	h.unregister(c)
	log.Printf("node %d (%s) disconnected", node.ID, node.Name)
}

func (h *Hub) register(c *nodeConn) {
	h.mu.Lock()
	if old := h.conns[c.nodeID]; old != nil {
		old.close()
	}
	h.conns[c.nodeID] = c
	h.mu.Unlock()
}

func (h *Hub) unregister(c *nodeConn) {
	h.mu.Lock()
	removed := h.conns[c.nodeID] == c
	if removed {
		delete(h.conns, c.nodeID)
	}
	h.mu.Unlock()
	c.close()
	if removed {
		h.scheduleOfflineNotice(c.nodeID)
	}
}

func (c *nodeConn) close() {
	c.once.Do(func() {
		close(c.send)
		c.ws.Close()
	})
}

func (c *nodeConn) writeLoop() {
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case m, ok := <-c.send:
			if !ok {
				return
			}
			c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.ws.WriteJSON(m); err != nil {
				return
			}
		case <-ping.C:
			c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}
}

func (h *Hub) readLoop(c *nodeConn) {
	c.ws.SetReadLimit(1 << 20)
	c.ws.SetPongHandler(func(string) error { return nil })
	for {
		var m wire.Msg
		if err := c.ws.ReadJSON(&m); err != nil {
			return
		}
		switch m.Type {
		case wire.TypeHeartbeat:
			_ = h.app.store.TouchNode(c.nodeID)
			if m.Metrics != nil {
				h.app.setNodeMetrics(c.nodeID, m.Metrics)
			}
		case wire.TypeTraffic:
			h.app.ingestTraffic(c.nodeID, m.Seq, m.Items)
			// Ack the seq so the agent can drop this batch. Ack even for a
			// duplicate (ingestTraffic dedups) so a lost prior ack resolves.
			c.trySend(wire.Msg{Type: wire.TypeAck, Seq: m.Seq})
		case wire.TypeDevices:
			h.app.ingestDevices(c.nodeID, m.Devices)
		}
	}
}

// trySend queues a message without blocking and reports whether it was queued.
// A false result means the node is gone or its write queue is backed up, and the
// message was dropped — callers that track delivery must not assume it arrived.
func (c *nodeConn) trySend(m wire.Msg) (sent bool) {
	defer func() {
		if recover() != nil {
			sent = false // send on closed channel during shutdown
		}
	}()
	select {
	case c.send <- m:
		return true
	default:
		return false
	}
}

// PushConfig regenerates and sends the Xray config for one node if it is online,
// reporting whether the config was handed to that node's write queue.
func (h *Hub) PushConfig(nodeID int64) bool {
	h.mu.RLock()
	c := h.conns[nodeID]
	h.mu.RUnlock()
	if c == nil {
		return false
	}
	node, err := h.app.store.GetNode(nodeID)
	if err != nil {
		return false
	}
	users, err := h.app.store.UsersForNode(nodeID)
	if err != nil {
		return false
	}
	cfg := h.app.buildXrayConfig(node, users)
	raw, err := json.Marshal(cfg)
	if err != nil {
		return false
	}
	return c.trySend(wire.Msg{Type: wire.TypeConfig, Config: raw, TLSDomain: node.TLSDomain})
}

// PushAll refreshes config on every connected node.
func (h *Hub) PushAll() {
	h.mu.RLock()
	ids := make([]int64, 0, len(h.conns))
	for id := range h.conns {
		ids = append(ids, id)
	}
	h.mu.RUnlock()
	for _, id := range ids {
		h.PushConfig(id)
	}
}

// OnlineNodeIDs lists currently connected nodes.
func (h *Hub) OnlineNodeIDs() map[int64]bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[int64]bool, len(h.conns))
	for id := range h.conns {
		out[id] = true
	}
	return out
}
