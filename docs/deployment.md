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
./deploy.sh reset-admin           reset the admin password from .env
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

Like a node, this **pulls** a prebuilt image
(`ghcr.io/mxlapan/xuanwu-panel:latest`, built for amd64 and arm64 by CI on every
push to `main`) and falls back to building locally when it cannot be pulled;
`--build` forces the local build and `PANEL_IMAGE` pins a different tag. Pulling
also shortens the update outage, since the panel is down for a container restart
rather than for a restart plus a Go build.

The panel's own build is stamped in the same way as an agent's and shown under
the sidebar logo (and in its startup log), so the running panel says which commit
it came from. `dev` means a locally built panel.

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

`./deploy.sh node` **pulls** the prebuilt agent image
(`ghcr.io/mxlapan/xuanwu-agent:latest`, built for amd64 and arm64 by CI on every
push to `main`). Nodes therefore need no Go toolchain: building the agent on the
node instead meant downloading ~950 MB and compiling for a couple of minutes to
produce an 80 MB image. The agent build is the leaner of the two — it skips the
panel's SQLite driver, which the panel image genuinely needs. Compose falls back to a local build whenever the image
cannot be pulled, so an unreachable registry costs only time; pass `--build` to
force the local build, or set `AGENT_IMAGE` to pin a different tag (each build is
also pushed as `:<commit-sha>`, so a rollback is one variable away).

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
   Renewals are checked twice a day with random jitter, so the node log stays
   quiet between them.
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

---

## Updating

Panel and agents are **version-independent**: added protocol fields are
`omitempty` and unknown ones are ignored, so a new panel works with an old agent
and vice versa. Update in any order, at any pace — there is no window where the
two must match.

```bash
./deploy.sh backup                 # snapshot the panel DB first
git pull && ./deploy.sh panel
```

Updating the panel interrupts **nobody**: each node's Xray keeps serving while
the panel is down. Agents reconnect with backoff, and their traffic buffer is
durable and de-duplicated by sequence number, so no usage is lost or double
counted. Only quota enforcement, subscription links and the admin UI pause for
the length of the restart.

Then update the nodes, one at a time so a bad rollout shows up on one node
instead of all of them (on each node host):

```bash
git pull && ./deploy.sh node
./deploy.sh logs node              # expect "connected to panel"
```

The agent itself comes from the registry, so this step re-pulls the image even
when nothing changed in the checkout; `git pull` is what picks up compose and
config changes. Each node's **agent build** is shown in the Nodes tab, so check
there that a node actually picked the new image up. It is the short commit the
image was built from; the matching image is
`ghcr.io/mxlapan/xuanwu-agent:<full sha>`, which is what `AGENT_IMAGE` takes to
roll one node back.

Restarting an agent does **not** restart Xray. The agent recovers what Xray is
running from `data/xray-baseline.json` and applies the panel's first config push
live over gRPC — look for `recovered xray baseline` followed by `config updated
live over gRPC (no restart)` in the log. Existing client connections survive the
update. Xray is still restarted when the config genuinely changes beyond its user
list (REALITY parameters, a new TLS domain); that one is unavoidable.

To roll back, check out the previous revision and re-run the same command.
Version independence applies here too: a rolled-back node keeps working against
the current panel.
