package agent

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/vless"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// inbound tags that carry the proxy users; must match nodeconf.Build.
const (
	tagTLS     = "vless-xtls-vision"
	tagReality = "vless-reality-vision"
)

// liveState is the agent's view of what Xray is currently running, so it can
// diff a new config and apply only the user changes over gRPC. It is mirrored to
// disk (see baseline.go) so an agent restart does not lose it.
type liveState struct {
	mu       sync.Mutex
	path     string                       // durable copy; "" disables persistence
	skeleton string                       // config JSON with client lists emptied
	clients  map[string]map[string]string // tag -> email -> uuid
	sha      string                       // sha256 of the config file this describes
}

func newLiveState(path string) *liveState {
	return &liveState{path: path, clients: map[string]map[string]string{}}
}

func (s *liveState) set(skeleton string, clients map[string]map[string]string, sha string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skeleton = skeleton
	s.clients = clients
	s.sha = sha
	s.persist()
}

// parseClients splits a config into its client sets (per relevant inbound tag)
// and a "skeleton" — the same config with those client arrays emptied. Two
// configs that differ only in users produce the same skeleton.
func parseClients(raw json.RawMessage) (skeleton string, clients map[string]map[string]string, ok bool) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", nil, false
	}
	inbounds, ok := m["inbounds"].([]any)
	if !ok {
		return "", nil, false
	}
	clients = map[string]map[string]string{}
	for _, ib := range inbounds {
		inb, ok := ib.(map[string]any)
		if !ok {
			continue
		}
		tag, _ := inb["tag"].(string)
		if tag != tagTLS && tag != tagReality {
			continue
		}
		settings, ok := inb["settings"].(map[string]any)
		if !ok {
			continue
		}
		arr, _ := settings["clients"].([]any)
		set := map[string]string{}
		for _, c := range arr {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			email, _ := cm["email"].(string)
			id, _ := cm["id"].(string)
			if email != "" {
				set[email] = id
			}
		}
		clients[tag] = set
		settings["clients"] = []any{} // empty for the skeleton
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", nil, false
	}
	return string(b), clients, true
}

// liveApply applies only the user delta over gRPC when nothing but users changed
// relative to the recorded baseline. Returns false to request a restart instead.
func (c *Config) liveApply(skeleton string, clients map[string]map[string]string, sha string) bool {
	c.live.mu.Lock()
	defer c.live.mu.Unlock()
	if c.live.skeleton == "" || c.live.skeleton != skeleton {
		return false // no baseline, or non-user parts changed -> restart
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, err := grpc.NewClient(c.GRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Printf("grpc dial %s: %v", c.GRPCAddr, err)
		return false
	}
	defer conn.Close()
	hs := command.NewHandlerServiceClient(conn)

	for _, tag := range []string{tagTLS, tagReality} {
		oldSet := c.live.clients[tag]
		newSet := clients[tag]
		// additions / changes
		for email, id := range newSet {
			if oldID, had := oldSet[email]; had && oldID == id {
				continue
			}
			if _, had := oldSet[email]; had {
				// uuid changed: remove then re-add
				if !removeUser(ctx, hs, tag, email) {
					return false
				}
			}
			if !addUser(ctx, hs, tag, email, id) {
				return false
			}
		}
		// removals
		for email := range oldSet {
			if _, keep := newSet[email]; !keep {
				if !removeUser(ctx, hs, tag, email) {
					return false
				}
			}
		}
	}

	c.live.skeleton = skeleton
	c.live.clients = clients
	c.live.sha = sha
	c.live.persist()
	return true
}

func addUser(ctx context.Context, hs command.HandlerServiceClient, tag, email, id string) bool {
	_, err := hs.AlterInbound(ctx, &command.AlterInboundRequest{
		Tag: tag,
		Operation: serial.ToTypedMessage(&command.AddUserOperation{
			User: &protocol.User{
				Level: 0,
				Email: email,
				Account: serial.ToTypedMessage(&vless.Account{
					Id:   id,
					Flow: "xtls-rprx-vision",
				}),
			},
		}),
	})
	if err != nil {
		log.Printf("grpc add user %s/%s: %v", tag, email, err)
		return false
	}
	return true
}

func removeUser(ctx context.Context, hs command.HandlerServiceClient, tag, email string) bool {
	_, err := hs.AlterInbound(ctx, &command.AlterInboundRequest{
		Tag:       tag,
		Operation: serial.ToTypedMessage(&command.RemoveUserOperation{Email: email}),
	})
	if err != nil {
		log.Printf("grpc remove user %s/%s: %v", tag, email, err)
		return false
	}
	return true
}
