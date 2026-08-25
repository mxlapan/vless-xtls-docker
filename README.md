# 玄武 Xuanwu — multi-node edge management

A self-hosted system for operating a fleet of TLS edge nodes from a single
control plane: a central panel, a self-service user portal, and a Telegram bot.
Each node terminates connections on port 443 and routes them with nginx SNI
preread, so ordinary and handshake-mimicking transports share the same port.

Everything is Go — two static binaries, `panel` and `agent` — plus an unchanged
nginx + engine Docker stack on each node.

## Two ways to deploy

- **Panel + managed nodes** — a central **control panel** (web UI + Telegram
  bot) manages users, quotas and subscriptions across any number of nodes. Each
  node runs a lightweight **agent** that dials the panel over an outbound
  WebSocket, applies the node config the panel generates, and reports traffic
  and devices. **The panel is a standalone service** — deploy it by itself and
  add nodes later.
- **Standalone node (no panel)** — a single server with local user management,
  like a classic one-box setup.

```
                        ┌───────────────────────────┐
   admin browser ─────► │  PANEL  (standalone)       │
   user portal ───────► │   REST API + embedded SPA  │
   user /sub/{token} ─► │   node WebSocket hub       │
   Telegram bot ──────► │   SQLite + daily backups   │
                        └──────────────▲─────────────┘
                                       │ outbound WS + node token
              ┌────────────────────────┼────────────────────────┐
        ┌─────┴─────┐            ┌──────┴────┐             ┌──────┴────┐
        │ node A    │            │ node B    │             │ node C    │
        │ agent     │            │ agent     │             │ agent     │
        │ nginx +   │            │ nginx +   │             │ nginx +   │
        │ engine    │            │ engine    │             │ engine    │
        └───────────┘            └───────────┘             └───────────┘
```

## Feature highlights

- **Central user management** — quotas (data + expiry), monthly auto-reset,
  enable/disable, enforced across all nodes.
- **Restart-free updates** — the agent applies user add/remove to the running
  engine over its management API (gRPC); it only restarts the container when
  non-user settings change (or a TLS cert is renewed).
- **Unified subscriptions** — one link per user across all their nodes, in
  base64 and the common YAML/JSON client formats; one-tap import + QR.
- **Self-service portal** — users sign in to see usage/expiry, copy links, scan
  QR codes; admin-set passwords are temporary and **force a change on first
  login**.
- **Durable traffic accounting** — ack-gated, persisted buffer + per-node
  dedup, so a dropped connection never loses a reporting window.
- **Device tracking** — distinct source IPs per user, admin-only.
- **Security** — admin 2FA (TOTP), a strong password policy, session
  revocation, security headers, multiple admin accounts, an audit log, and
  at-rest encryption of secret fields. Nodes reach Docker only through an
  allow-listed proxy, never the raw socket.
- **Telegram** — a management bot *and* push notifications (node up/down, user
  disabled/over-quota/expired).
- **Ops** — consistent DB backups (endpoint + daily snapshots), graceful
  shutdown, optional ACME/Let's Encrypt for node certs with hot-reload.

## Quick start

**Panel (standalone):**

```bash
cd deploy/panel
cp .env.example .env      # set PANEL_ADMIN_PASS (strong), PANEL_JWT_SECRET, PANEL_PUBLIC_URL
cd ../.. && ./deploy.sh panel
# open PANEL_PUBLIC_URL and log in
```

**Add a managed node:** in the UI create a node (click *Generate keys*), open
*Install* for the token + one-liner, then fill in the node's `.env` and start it:

```bash
cd deploy/node
cp .env.example .env      # MODE=managed, plus the panel URL, node token and domain
cd ../.. && ./deploy.sh node
```

**Standalone node (no panel):**

```bash
cd deploy/node && cp .env.example .env   # MODE=standalone, plus the node's own settings
../../deploy.sh keys                      # generate a node keypair
cd ../.. && ./deploy.sh standalone
./deploy.sh user add alice                # prints alice's client links
```

> Put the panel behind **HTTPS** in production — session cookies are only marked
> `Secure` when `PANEL_PUBLIC_URL` is `https://`.

## Documentation

Full docs live in [`docs/`](docs/):

| Topic | |
|---|---|
| [Deployment](docs/deployment.md) | panel (standalone), managed & standalone nodes, TLS, ACME, backups |
| [Configuration](docs/configuration.md) | every environment variable, panel + node |
| [Users & subscriptions](docs/users.md) | quotas, expiry, monthly reset, sub formats, rotation, devices, notes |
| [Self-service portal](docs/portal.md) | user login, forced password change, password policy |
| [Telegram](docs/telegram.md) | bot commands + push notifications + setup |
| [Security](docs/security.md) | auth model, 2FA, sessions, multi-admin, audit log, threat model |
| [Protocol](docs/PROTOCOL.md) | panel ↔ agent WebSocket wire protocol |
| [Troubleshooting](docs/troubleshooting.md) | common problems |

## Repository layout

```
.
├── deploy.sh                 # dispatcher: panel | node | standalone | user | keys | reset-admin | backup
├── cmd/{panel,agent}/        # entrypoints
├── internal/
│   ├── panel/                # API, hub, subscriptions, traffic, devices, bot, notify, web SPA
│   ├── agent/                # WS client, standalone mode, live gRPC edits, docker proxy, watchers
│   ├── nodeconf/             # shared node config generator
│   ├── nodekey/              # node keypair generation
│   └── wire/                 # shared panel↔agent message types
├── deploy/{panel,node}/      # docker-compose + .env.example
├── Dockerfile.{panel,agent}
└── docs/
```

## Build & test

```bash
go build ./...
go vet ./...
go test ./...
```
