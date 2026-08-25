# Deployment

Xuanwu ships two independent Docker stacks:

- `deploy/panel/` — the **panel** (one container). Self-contained; needs no node.
- `deploy/node/` — a **node**: `nginx` (SNI split) + `xray` + `agent`, in either
  `managed` or `standalone` mode, with an optional `acme` sidecar.

The `./deploy.sh` dispatcher wraps the common commands:

```
./deploy.sh panel                 deploy the control panel
./deploy.sh node                  deploy a panel-managed node (MODE=managed)
./deploy.sh standalone            deploy a single node with no panel
./deploy.sh user add|rm|list …    (standalone) manage local users
./deploy.sh keys                  generate a REALITY x25519 keypair
./deploy.sh backup [outfile]      copy the latest panel DB snapshot out
./deploy.sh down  [panel|node]    stop a stack
./deploy.sh logs  [panel|node]    follow logs
```

See [configuration.md](configuration.md) for every environment variable.

---

## Panel (standalone)

The panel is a single stateless-ish Go service backed by SQLite. It runs
completely on its own; nodes connect to it later over an outbound WebSocket.

```bash
cd deploy/panel
cp .env.example .env
# edit .env: PANEL_ADMIN_PASS (strong!), PANEL_JWT_SECRET
cd ../..
./deploy.sh panel
```

The `.env` is **minimal** — only bootstrap secrets:

- `PANEL_ADMIN_PASS` — the admin password. The panel **refuses to boot** with a
  weak one (must be ≥8 chars with upper, lower, digit and special). For local
  testing only, `PANEL_ALLOW_WEAK_PASS=1` bypasses the check.
- `PANEL_JWT_SECRET` — **required**; generate with `openssl rand -hex 32`. It
  signs session cookies and keys the at-rest encryption of secret DB columns, so
  keep it stable: changing it logs everyone out and makes existing encrypted
  values unrecoverable.

Everything else is configured **in the panel** after first login:

- **Settings** → **Public URL** (used in subscription links + the node install
  command), **Cookie security**, **Daily backups to keep**.
- **Security** → **Telegram** (bot token + admin chat IDs).

Data (SQLite DB + daily backups) lives on the host at **`deploy/panel/data/`**
(a bind mount). Protect that directory — it holds secrets. See
[configuration.md](configuration.md).

### Put it behind HTTPS

The panel serves plain HTTP on `:8088` (host port `PANEL_PORT`). **Terminate TLS
in front of it** (Caddy, nginx, Traefik, a cloud LB…). When `PANEL_PUBLIC_URL`
is `https://`, the session cookie is automatically marked `Secure`; force it
either way with `PANEL_COOKIE_SECURE=true|false`.

Minimal Caddy example:

```
panel.example.com {
    reverse_proxy 127.0.0.1:8088
}
```

The panel's health probe is `GET /healthz` (returns `ok`, unauthenticated).

---

## Managed node

A managed node's agent dials the panel and receives its Xray config; you never
edit the node's Xray config by hand.

1. In the panel UI, **create a node** (Nodes → Add node). Fill the REALITY
   section (dest + serverName, *Generate REALITY keypair*) and/or set a TLS
   domain to enable TLS-Vision — leave a section blank to skip it. Save.
2. Open the node's **Install** dialog to copy its **token** and the one-liner.
3. On the node host:

   ```bash
   cd deploy/node
   cp .env.example .env
   # MODE=managed, PANEL_URL=https://panel.example.com, NODE_TOKEN=…,
   # DOMAIN=node1.example.com, REALITY_SERVER_NAME=www.microsoft.com
   cd ../..
   ./deploy.sh node
   ```

The agent connects out, registers with its token, receives config, and starts
reporting traffic + devices. Assign users to the node in the UI and the panel
pushes an updated config automatically (usually with **no Xray restart** — see
[users.md](users.md)).

Ports on a node: `nginx` publishes **:443** (and **:80** only if the ACME
sidecar is enabled). Xray's `10443/10444/10085` are internal to the compose
network and must **never** be published to the host.

---

## Standalone node (no panel)

```bash
cd deploy/node
cp .env.example .env
# MODE=standalone, DOMAIN, ADDRESS, REALITY_DEST, REALITY_SERVER_NAME,
# REALITY_PRIVATE_KEY, REALITY_PUBLIC_KEY, REALITY_SHORT_ID
../../deploy.sh keys          # prints a REALITY private/public key to paste in
cd ../..
./deploy.sh standalone

./deploy.sh user add alice    # prints alice's vless:// share links
./deploy.sh user list
./deploy.sh user rm alice
```

Users are stored locally in `deploy/node/data/users.json`. There is no panel,
portal, quota enforcement or traffic accounting in this mode — it is a simple
one-box setup.

---

## TLS certificates

The **TLS-Vision** inbound (for clients using your real domain as SNI) needs a
certificate at `deploy/node/certs/{fullchain,privkey}.pem`. The **REALITY**
inbound needs no certificate.

You have three options:

1. **Bring your own** — drop `fullchain.pem` + `privkey.pem` into
   `deploy/node/certs/`. When the file changes, the agent **hot-reloads Xray
   automatically** (it watches `XRAY_CERT`).
2. **Automatic (ACME/Let's Encrypt)** — no extra config on a managed node: set
   the node's **TLS domain in the panel** and the agent tells the always-on
   `acme` sidecar, which issues + renews the cert. (Standalone: set `DOMAIN` in
   `.env` instead.) The domain must resolve publicly to this host and port 80 must
   be reachable (HTTP-01); the cert lands in `./certs` and the agent hot-reloads
   Xray. `ACME_EMAIL` is optional — a random one is generated if unset. For hosts
   behind NAT/CDN, switch the issue command to a DNS-01 provider (see acme.sh docs).
3. **REALITY only** — leave the TLS domain blank (in the panel, or `DOMAIN`
   unset). The node runs REALITY only and needs no certificate; if a TLS domain
   is set but the cert is missing, the agent automatically disables just the
   TLS-Vision inbound (REALITY keeps working) until a cert appears.

---

## Backups

The panel keeps a **consistent** copy of its SQLite database (using `VACUUM
INTO`, safe under concurrent writes):

- **Scheduled** — a snapshot is written to
  `deploy/panel/data/backups/panel-YYYYMMDD.db` on boot and daily, keeping the
  newest **Daily backups to keep** (Settings, default 7; `0` disables).
- **On demand (UI)** — Dashboard → **Backup DB** downloads a fresh snapshot.
- **On demand (API)** — `GET /api/backup` (admin-authenticated) streams a
  `.db` file.
- **From the host** — `./deploy.sh backup [outfile]` copies the latest scheduled
  snapshot out of `deploy/panel/data/backups/`.

To restore, stop the panel and replace `deploy/panel/data/panel.db` with a
snapshot, then start it again.
