# Configuration reference

The **panel keeps only a minimal bootstrap `.env`**; most configuration is done
in the panel UI (**Settings** and **Security → Telegram**) and stored in the
database. The environment values below are used to bootstrap and to *seed* the
initial in-panel values.

Configured **in the panel** (not env): public URL, cookie-Secure mode, backup
retention, an optional Clash export template, and Telegram token / admin IDs.

The **Clash template** (Settings) lets you keep your own DNS / rules / routing:
paste a full Clash config with `{{PROXIES}}` on its own line under `proxies:` and
`{{PROXY_NAMES}}` inside each proxy-group's `proxies: [ ]`; the panel injects the
generated nodes and leaves everything else untouched. Blank = built-in minimal
config.

## Panel `.env` (minimal bootstrap)

| Variable | Default | Purpose |
|---|---|---|
| `PANEL_PORT` | `8088` | Host port published by compose. |
| `PANEL_ADMIN_USER` | `admin` | Bootstrap admin username. |
| `PANEL_ADMIN_PASS` | *(none)* | Bootstrap admin password (required; strong). Seeds the account on first boot only — see [reset](troubleshooting.md#forgot-the-admin-password). |
| `PANEL_JWT_SECRET` | *(none)* | **Required.** Signs session cookies and keys the at-rest encryption of secret DB columns (`openssl rand -hex 32`). |

Optional seeds / advanced (usually left unset): `PANEL_PUBLIC_URL`,
`PANEL_COOKIE_SECURE`, `PANEL_BACKUP_KEEP`, `TELEGRAM_BOT_TOKEN`,
`TELEGRAM_ADMIN_IDS` (all editable in the panel afterwards); `PANEL_ALLOW_WEAK_PASS`
(local testing), `PANEL_LISTEN`, `PANEL_DB_PATH`, `PANEL_BACKUP_DIR` (internal).

Panel state lives in the host bind mount **`deploy/panel/data/`** (SQLite DB +
daily backups).

## All panel variables (including seeds/internal)

| Variable | Default | Purpose |
|---|---|---|
| `PANEL_PORT` | `8088` | Host port published by compose (maps to container `:8088`). |
| `PANEL_LISTEN` | `:8088` | Address the panel binds inside the container. |
| `PANEL_DB_PATH` | `/data/panel.db` | SQLite database path (inside the `deploy/panel/data/` bind mount). |
| `PANEL_PUBLIC_URL` | `http://localhost:8088` | External base URL; used in subscription links, the node install command, and to decide cookie `Secure`. Use `https://…` in production. |
| `PANEL_ADMIN_USER` | `admin` | Bootstrap admin username (created on first boot). |
| `PANEL_ADMIN_PASS` | *(none)* | Bootstrap admin password. **Must** be ≥8 chars with upper, lower, digit and special, or the panel refuses to start. Seeds the account on first boot only; afterwards the stored password wins — change a forgotten one with `./deploy.sh reset-admin`. |
| `PANEL_ALLOW_WEAK_PASS` | `false` | Local-testing escape hatch for the password guard. Never set in production. |
| `PANEL_JWT_SECRET` | *(none)* | **Required**, ≥16 chars (`openssl rand -hex 32`); the panel refuses to start without one. Signs session cookies **and** derives the key that encrypts secret DB columns, so keep it stable: changing it logs everyone out and makes existing encrypted values unrecoverable. |
| `PANEL_COOKIE_SECURE` | *(auto)* | Force the session cookie `Secure` flag. Defaults to on when `PANEL_PUBLIC_URL` is `https://`. |
| `PANEL_BACKUP_DIR` | `/data/backups` | Directory for daily DB snapshots; empty string disables scheduled backups. |
| `PANEL_BACKUP_KEEP` | `7` | Number of daily snapshots to retain. |
| `TELEGRAM_BOT_TOKEN` | *(empty)* | **Initial seed** for the Telegram bot token (from @BotFather). Editable in the panel (Security → Telegram), which overrides this and persists to the DB. |
| `TELEGRAM_ADMIN_IDS` | *(empty)* | **Initial seed** for the comma-separated admin chat IDs. Also editable in the panel. |

## Node / agent (`deploy/node/.env`)

### Mode

| Variable | Default | Purpose |
|---|---|---|
| `MODE` | `managed` | `managed` (connect to a panel) or `standalone` (local users, no panel). |

### Managed mode

| Variable | Default | Purpose |
|---|---|---|
| `PANEL_URL` | *(required)* | Panel base URL the agent dials (`https://panel.example.com`). |
| `NODE_TOKEN` | *(required)* | Node token from the panel's *Install* screen. |
| `REALITY_SERVER_NAME` | *(required)* | SNI that nginx routes to the REALITY inbound; other SNIs go to TLS-Vision. |
| `DOMAIN` | *(required)* | Your TLS domain (nginx `server_name`, TLS-Vision SNI). |
| `STATS_INTERVAL` | `60` | Seconds between traffic + device samples. |

In managed mode the REALITY keys and per-user config come **from the panel**;
you do not set `REALITY_PRIVATE_KEY` etc. here.

### Standalone mode

| Variable | Default | Purpose |
|---|---|---|
| `ADDRESS` | *(none)* | Public host/IP used in generated share links. |
| `DOMAIN` | *(none)* | TLS domain for the TLS-Vision link + nginx. |
| `REALITY_DEST` | *(none)* | REALITY `dest` (e.g. `www.microsoft.com:443`). |
| `REALITY_SERVER_NAME` | *(none)* | REALITY `serverName` / SNI. |
| `REALITY_PRIVATE_KEY` | *(none)* | REALITY private key (`./deploy.sh keys`). |
| `REALITY_PUBLIC_KEY` | *(none)* | REALITY public key (used in share links). |
| `REALITY_SHORT_ID` | *(none)* | REALITY shortId. |
| `XUANWU_USERS_FILE` | `/data/users.json` | Local user database. |

### Common agent internals (rarely changed)

| Variable | Default | Purpose |
|---|---|---|
| `XRAY_CONFIG` | `/etc/xray/config.json` | Shared config file the agent writes. |
| `XRAY_CONTAINER` | `xray` | Xray container name (for `docker restart`/`exec`). |
| `XRAY_API` | `127.0.0.1:10085` | Xray stats API used via `docker exec` inside the xray container. |
| `XRAY_GRPC` | `xray:10085` | Xray API reachable from the agent for live (restart-free) user edits. |
| `XRAY_CERT` | `/etc/xray/certs/fullchain.pem` | TLS cert file watched for renewals (triggers a hot reload). |
| `XRAY_ACCESS_LOG` | `/var/log/xray/access.log` | Access log parsed for device tracking. |

### ACME sidecar (issues certs automatically)

The `acme` sidecar always runs and stays idle until a **TLS domain** is set. The
agent publishes that domain to `data/acme/domain` — from the **panel** (managed:
set the node's *TLS domain* in the UI) or from **`DOMAIN`** in `.env` (standalone).
A managed node therefore never repeats the domain in `.env`. It issues + renews
via HTTP-01 (port 80, so the domain must resolve to this host) and the agent
hot-reloads Xray.

The domain file is polled every 60s (a local read), but **renewals are checked
twice a day**, on Let's Encrypt's own guidance, with up to an hour of random
jitter so a fleet does not call the CA in lockstep. A renewal check reaches the
ACME directory even when it goes on to skip, and acme.sh's self-upgrade runs
along with it — doing that every minute got the CA and GitHub polled 1440 times
a day per node and buried everything else in `./deploy.sh logs node`.

| Variable | Default | Purpose |
|---|---|---|
| `ACME_EMAIL` | *(random)* | Registration email; a random one is generated if unset. |
| `ACME_SERVER` | `letsencrypt` | CA (`letsencrypt` or `letsencrypt_test` for staging). |
| `DOMAIN` | *(none)* | **Standalone only** — TLS domain to issue for (managed gets it from the panel). |

### Image overrides

| Variable | Default |
|---|---|
| `AGENT_IMAGE` | `ghcr.io/mxlapan/xuanwu-agent:latest` |
| `PANEL_IMAGE` | `ghcr.io/mxlapan/xuanwu-panel:latest` |
| `XRAY_IMAGE` | `ghcr.io/xtls/xray-core:26.6.27` |
| `NGINX_IMAGE` | `nginx:1.30.3-alpine` |
| `ACME_IMAGE` | `neilpang/acme.sh:latest` |
