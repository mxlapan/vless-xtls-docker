# Troubleshooting

## Panel won't start: "PANEL_ADMIN_PASS is too weak"

The admin password must be ≥8 chars with an uppercase, lowercase, digit and
special character. Set a strong `PANEL_ADMIN_PASS` in `deploy/panel/.env`. For
local testing only, `PANEL_ALLOW_WEAK_PASS=1` bypasses the check.

## Sessions drop after a panel restart

`PANEL_JWT_SECRET` changed. It signs every session cookie, so a new value logs
everyone out — and because the same secret keys the at-rest encryption of secret
DB columns, the REALITY private keys, TOTP secrets and Telegram token stored
under the old one can no longer be decrypted either. Put the previous value back.
It can no longer be *unset*: the panel refuses to boot without one.

## Node shows offline in the panel

- Check the agent logs: `./deploy.sh logs node`.
- The agent dials `PANEL_URL` **outbound** — make sure it's correct and
  reachable from the node (DNS + firewall). Use the public `https://` URL, not a
  private/internal address the node can't resolve.
- `NODE_TOKEN` must match the token from the panel's *Install* dialog.
- Time skew can break TLS; keep clocks in sync.

## Node online but clients can't connect

- **TLS-Vision** needs a valid cert at `deploy/node/certs/{fullchain,privkey}.pem`.
  Missing/expired certs break that inbound (REALITY still works). See
  [deployment.md](deployment.md#tls-certificates).
- Confirm `REALITY_SERVER_NAME` matches the SNI in the client's REALITY link and
  the value nginx routes on.
- The user must be **active** (enabled, not expired, under quota) and **assigned
  to that node**.

## Cert renewed but Xray still serves the old one

The agent watches `XRAY_CERT` and re-applies the config on change (which reloads
Xray, and re-enables TLS-Vision if the cert had been missing); it polls every
~30s. Confirm the new cert actually replaced the file the agent mounts
(`deploy/node/certs/`), and check agent logs for `tls cert changed`.

## Node card shows no CPU / disk / uptime

Those readings come from the agent's heartbeat, and an agent older than the panel
does not send them — it reports only load average and memory, and the panel
leaves the rest out rather than showing zeros. Redeploy the node to pick up the
current agent: `./deploy.sh node` (or `./deploy.sh standalone`).

CPU is a delta between two heartbeats, so it reads 0% for the first ~20s after an
agent restart. Process and connection counts are not reported at all — see
[PROTOCOL.md](PROTOCOL.md#node-metrics-source) for why.

## Traffic isn't updating

- Traffic is sampled every `STATS_INTERVAL` seconds (default 60) and only sent
  when non-zero. Generate some traffic and wait a cycle.
- Check the agent can reach Xray's stats API: it runs `docker exec xray xray api
  statsquery …` inside the xray container.

## Device list / count is empty

Devices come from the Xray **access log** (`XRAY_ACCESS_LOG`). Ensure the log is
being written (it is enabled by default) and that the agent mounts the log
directory read-only (it does in the shipped compose). Counts cover the last 30
days of distinct source IPs.

## Telegram: nothing happens

- Set **both** `TELEGRAM_BOT_TOKEN` and `TELEGRAM_ADMIN_IDS` in
  `deploy/panel/.env`, then recreate: `./deploy.sh panel`.
- Your numeric chat id must be in `TELEGRAM_ADMIN_IDS`. Message the bot; if it
  replies "Unauthorized … your chat id is N", add `N`.
- Use **Security → Send test message** in the panel to verify end-to-end.

## Healthcheck / reverse proxy

The panel's liveness endpoint is `GET /healthz` (unauthenticated, returns `ok`).
Point your load balancer / compose healthcheck at it, not at an authenticated
route.

## Forgot the admin password

`PANEL_ADMIN_PASS` seeds the admin account on the **first boot only**; after that
the database is authoritative, so editing `.env` and recreating the panel does
*not* change a stored password. Reset it explicitly instead: put the new password
in `deploy/panel/.env` (`PANEL_ADMIN_PASS=…`, strong), then run

```bash
./deploy.sh reset-admin
```

The panel does not have to be stopped — the one-off container writes to the same
database and the new password works immediately. All existing sessions for that
admin are revoked; other admin accounts, users, nodes and traffic are untouched.
The account is recreated if it had been deleted.

Two-factor authentication deliberately survives a plain reset. If you lost the
authenticator too, drop it as well:

```bash
./deploy.sh reset-admin -clear-2fa
```

The subcommand is a thin wrapper; anywhere `deploy.sh` isn't handy:

```bash
cd deploy/panel && docker compose run --rm --build panel -reset-admin
```
