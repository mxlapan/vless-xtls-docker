# Security

## Authentication model

There are two completely separate identities:

| | Admin | Portal user |
|---|---|---|
| Credential | username + password (bcrypt) | username + portal password (bcrypt) |
| Cookie | `xuanwu_session` | `xuanwu_portal` |
| Session role | `admin` | `user` |
| Can reach | all admin APIs | only its own account view |

Sessions are stateless, signed tokens: `role | subject | epoch | expiry | sig`,
HMAC-signed with `PANEL_JWT_SECRET`. The **role is part of the signature**, so a
portal token can never be replayed as an admin token (and vice-versa). Node
agents authenticate separately with a per-node token over the WebSocket — never
with a cookie.

## Password policy

Every password (admin bootstrap, admin accounts, portal passwords set by an
admin, and portal self-service changes) must be **≥8 chars with an uppercase, a
lowercase, a digit, and a special character**. The panel **refuses to boot** if
`PANEL_ADMIN_PASS` is weak (override for local testing only with
`PANEL_ALLOW_WEAK_PASS=1`). Enforced server-side; existing passwords are
grandfathered until changed.

## Admin two-factor (TOTP)

Under **Security**, an admin can enable TOTP (RFC 6238, compatible with any
authenticator app). Once enabled, login requires the current 6-digit code in
addition to the password. Disabling requires a valid code, so a hijacked session
can't turn it off.

## Recovering admin access

There is no reset link and no recovery e-mail: a forgotten admin password (or a
lost authenticator) is recovered **from the host** that holds the database, with
`./deploy.sh reset-admin [-clear-2fa]`. It re-seeds the account from
`PANEL_ADMIN_PASS`, recreating it if it was deleted, and revokes its sessions;
`-clear-2fa` additionally drops the TOTP enrolment, so a plain reset can never
silently weaken two-factor login. Every reset is written to the audit log as
`admin.reset` by actor `cli`. Shell access to the panel host is therefore
equivalent to full panel access — guard it accordingly.

## Session revocation

Each account carries a **session epoch** embedded in its tokens and checked on
every request. Bumping it invalidates all outstanding sessions:

- **Sign out everywhere** (Security modal) revokes all of an admin's sessions.
- Changing a user's **portal password** (by the admin, or via the user's forced
  change) revokes that user's other portal sessions.
- Deleting an admin revokes its sessions immediately.

## Multiple admins & audit log

- **Manage admins** (Security modal) — list, add, delete admins, and change
  passwords. The last admin can't be deleted. A password change bumps that
  admin's session epoch.
- **Audit log** — sensitive actions (logins, user/node create/update/delete,
  rotations, portal-password changes, 2FA changes, admin management) are recorded
  with actor, action, detail and timestamp, viewable in the Security modal.

## Transport & headers

- Serve the panel behind **HTTPS**; the session cookie is `HttpOnly`,
  `SameSite=Lax`, and `Secure` when the public URL is https. It is **not**
  `Secure` on plain HTTP — do not run the panel on plain HTTP in production.
- All responses carry hardening headers: a strict `Content-Security-Policy`
  (self-hosted assets only), `X-Content-Type-Options: nosniff`, `X-Frame-Options:
  DENY`, and `Referrer-Policy: no-referrer`.
- Login is rate-limited per source IP and per targeted username (8 failures /
  5 min → temporary lockout), for both admin and portal login.

## Data at rest

- The SQLite DB holds bcrypt password hashes, node tokens, REALITY private keys,
  and subscription tokens. The secret columns (REALITY private keys, TOTP
  secrets, the Telegram token) are encrypted with AES-256-GCM under a key derived
  from `PANEL_JWT_SECRET`; the rest is plaintext, so protect
  `deploy/panel/data/` and your backups accordingly.
- Subscription tokens and node tokens are high-entropy random values; rotate a
  user's sub token/UUID from the UI if one leaks.

## Threat model & accepted risks

- **Agent has the node's Docker socket.** The agent restarts/execs the Xray
  container via `/var/run/docker.sock`, which is effectively root on the node
  host. This means the panel is fully trusted to control every node. Keep the
  panel and node tokens secret and the panel behind TLS.
- **Xray control API on the node network.** Xray's `10085` API (stats +
  add/remove user) has no auth of its own; it is protected only by network
  isolation. The compose file **exposes it only on the internal node network and
  never publishes it to the host** — keep it that way.
- **Device geolocation** is intentionally not implemented yet (it would send
  client IPs to a third party); only raw source IPs are recorded, admin-only.
- **Browsing destinations are never recorded** — device tracking captures source
  IP and inbound type only, from the access log.
- **Panel container runs as root.** To write the host bind-mounted `./data`, the
  panel process runs as root *inside its container*. That container is otherwise
  isolated — no Docker socket, no added capabilities, and only the `./data`
  mount — so this is low risk and consistent with the node containers.
- **`deploy/panel/data/` holds the database**, which contains bcrypt password
  hashes, node tokens, REALITY private keys, subscription tokens and the
  Telegram bot token. It is bind-mounted from the host; **protect the deploy
  directory** (e.g. `chmod 700`) on shared hosts, the same as you would protect
  `.env`. Backup snapshots are written `0644` so `./deploy.sh backup` can copy
  them.
- **Runtime config in the panel.** Public URL, cookie-Secure mode, backup
  retention and Telegram settings are stored in the DB and editable by any admin
  (audited). The `.env` only holds bootstrap secrets (admin password, JWT
  secret). Setting cookie-Secure to `off` disables the Secure flag — only do
  that behind a TLS-terminating proxy on an internal HTTP hop.
