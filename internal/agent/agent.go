// Package agent runs on each managed node. In managed mode it keeps an outbound
// WebSocket to the panel, applies the Xray config the panel pushes, and reports
// per-user traffic. In standalone mode (see standalone.go) it manages users
// locally with no panel.
package agent

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"xuanwu/internal/wire"
)

// Version identifies this agent build to the panel. It is stamped at build time
// with the commit the image was built from (see Dockerfile.agent), because a
// hand-maintained constant silently goes stale and then reports the same version
// for every build — which is exactly when you need to know which nodes are
// running old code.
var Version = "dev"

// Config holds the node-side settings, from environment variables.
type Config struct {
	PanelURL      string
	Token         string
	XrayConfig    string        // path to the shared config.json this agent writes
	XrayContainer string        // docker container name of xray
	GRPCAddr      string        // xray api reachable from the agent for live edits + stats, e.g. xray:10085
	CertPath      string        // TLS cert file to watch for renewals ("" disables)
	StatsInterval time.Duration // traffic sampling interval

	AccessLog      string // path to the Xray access log for device tracking
	NginxConf      string // nginx.conf the agent generates for SNI routing
	NginxContainer string // nginx container name (for reload)
	ACMEDomainFile string // file the agent publishes the TLS domain to for the acme sidecar

	live  *liveState      // in-memory baseline for restart-free (gRPC) config updates
	pend  *pendingTraffic // durable, ack-gated traffic buffer
	acc   *accessWatcher  // access-log device watcher
	apply *applyState     // serializes applyConfig + remembers the last full config
}

// applyState lives behind a pointer because Config is copied by value; it must be
// shared so config pushes and the cert watcher serialize on the same mutex.
type applyState struct {
	mu   sync.Mutex
	lmu  sync.Mutex
	last json.RawMessage // most recent full config, for cert-triggered re-apply
}

func (c *Config) setLastConfig(raw json.RawMessage) {
	c.apply.lmu.Lock()
	c.apply.last = append(json.RawMessage(nil), raw...)
	c.apply.lmu.Unlock()
}

func (c *Config) getLastConfig() json.RawMessage {
	c.apply.lmu.Lock()
	defer c.apply.lmu.Unlock()
	return c.apply.last
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDur(key string, def int) time.Duration {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return time.Duration(def) * time.Second
}

// ConfigFromEnv builds agent config from environment variables.
func ConfigFromEnv() Config {
	usersFile := env("XUANWU_USERS_FILE", "/data/users.json")
	c := Config{
		PanelURL:       env("PANEL_URL", ""),
		Token:          env("NODE_TOKEN", ""),
		XrayConfig:     env("XRAY_CONFIG", "/etc/xray/config.json"),
		XrayContainer:  env("XRAY_CONTAINER", "xray"),
		GRPCAddr:       env("XRAY_GRPC", "xray:10085"),
		CertPath:       env("XRAY_CERT", "/etc/xray/certs/fullchain.pem"),
		AccessLog:      env("XRAY_ACCESS_LOG", "/var/log/xray/access.log"),
		NginxConf:      env("NGINX_CONF", "/data/nginx/nginx.conf"),
		NginxContainer: env("NGINX_CONTAINER", "nginx-edge"),
		ACMEDomainFile: env("ACME_DOMAIN_FILE", "/data/acme/domain"),
		StatsInterval:  envDur("STATS_INTERVAL", 60),
		live:           newLiveState(baselinePath(usersFile)),
		pend:           loadPending(pendingPath(usersFile)),
		acc:            newAccessWatcher(env("XRAY_ACCESS_LOG", "/var/log/xray/access.log"), accessStatePath(usersFile)),
		apply:          &applyState{},
	}
	// Recover what Xray is already running, so restarting the agent (an update,
	// a crash) does not force a config-push restart that drops live connections.
	c.live.seed(c.XrayConfig)
	return c
}

// RunManaged connects to the panel and serves config/traffic until killed.
func RunManaged(c Config) error {
	if c.PanelURL == "" || c.Token == "" {
		return errRequired
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Seed a TLS-only nginx config so nginx can start; the panel's pushed config
	// updates the REALITY SNI once we connect.
	c.applyNginx("")
	go c.watchCert(ctx)

	// Exit cleanly on SIGTERM so `docker stop` doesn't wait for the kill timeout.
	// The traffic buffer is persisted on every change, so a stop never loses data.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		log.Printf("agent shutting down")
		os.Exit(0)
	}()

	backoff := time.Second
	for {
		if err := c.session(); err != nil {
			log.Printf("session ended: %v; reconnecting in %s", err, backoff)
		}
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

type configError string

func (e configError) Error() string { return string(e) }

const errRequired = configError("PANEL_URL and NODE_TOKEN are required")

func wsURL(panel string) (string, error) {
	u, err := url.Parse(panel)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/node/ws"
	return u.String(), nil
}

func (c *Config) session() error {
	endpoint, err := wsURL(c.PanelURL)
	if err != nil {
		return err
	}
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment}
	conn, _, err := dialer.Dial(endpoint, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	log.Printf("connected to panel at %s", endpoint)

	var wmu sync.Mutex
	send := func(m wire.Msg) error {
		wmu.Lock()
		defer wmu.Unlock()
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(m)
	}

	if err := send(wire.Msg{Type: wire.TypeRegister, Token: c.Token, Version: Version}); err != nil {
		return err
	}

	// Flush any traffic buffered from a previous (dropped) session first.
	if seq, items, ok := c.pend.batch(); ok {
		if err := send(wire.Msg{Type: wire.TypeTraffic, Seq: seq, Items: items}); err != nil {
			return err
		}
	}

	stop := make(chan struct{})
	var once sync.Once
	closeStop := func() { once.Do(func() { close(stop) }) }

	go func() {
		hb := time.NewTicker(20 * time.Second)
		st := time.NewTicker(c.StatsInterval)
		defer hb.Stop()
		defer st.Stop()
		for {
			select {
			case <-stop:
				return
			case <-hb.C:
				if err := send(wire.Msg{Type: wire.TypeHeartbeat, Metrics: c.collectMetrics()}); err != nil {
					closeStop()
					return
				}
			case <-st.C:
				// Collect into the durable buffer first, then (re)send the current
				// batch. The buffer is only cleared once the panel acks (below), so
				// a failed send here never loses the counters we just reset in Xray.
				c.pend.collect(c.collectTraffic())
				if seq, items, ok := c.pend.batch(); ok {
					if err := send(wire.Msg{Type: wire.TypeTraffic, Seq: seq, Items: items}); err != nil {
						closeStop()
						return
					}
				}
				if devs := c.acc.collect(); len(devs) > 0 {
					if err := send(wire.Msg{Type: wire.TypeDevices, Devices: devs}); err != nil {
						closeStop()
						return
					}
				}
			}
		}
	}()

	conn.SetReadLimit(1 << 20)
	for {
		var m wire.Msg
		if err := conn.ReadJSON(&m); err != nil {
			closeStop()
			return err
		}
		switch m.Type {
		case wire.TypeConfig:
			c.writeACMEDomain(m.TLSDomain)
			if err := c.applyConfig(m.Config); err != nil {
				log.Printf("apply config: %v", err)
			}
		case wire.TypeAck:
			c.pend.ack(m.Seq)
		}
	}
}
