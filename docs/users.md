# Users & subscriptions

(Panel mode. For standalone nodes, see `./deploy.sh user add|rm|list`.)

## Creating a user

**Users → Add user.** Fields:

- **Username** — unique, and doubles as the Xray "email" used for traffic
  attribution, so it **cannot be changed** later.
- **Data limit** — GB; `0` = unlimited.
- **Expires at** — optional date/time; empty = never.
- **Monthly reset day** — `1`–`28`, or `0` to disable. On that day (UTC) the
  user's `data_used` is auto-reset to zero, once per month.
- **Enabled** — the manual on/off switch.
- **Admin note** — a private remark (see below).
- **Nodes** — which nodes the user is provisioned onto.

On save the user gets a random UUID and a random subscription token, and the
panel pushes an updated config to the selected nodes.

A user is **active** (provisioned into node configs) only when: enabled, not
expired, and under quota. When any of those flips, the enforcement loop drops the
user from the affected nodes within ~30s (and immediately on a traffic report
that crosses the quota).

## Subscriptions

Each user has one unified subscription across all their assigned nodes. Open the
**link icon** on a user row (or the portal) to copy:

| Format | URL | Client |
|---|---|---|
| Base64 VLESS | `PANEL_PUBLIC_URL/sub/{token}` | v2rayN, most clients |
| Clash / Mihomo | `…/sub/{token}/clash` | Clash.Meta / Mihomo |
| sing-box | `…/sub/{token}/singbox` | sing-box |

Each node contributes up to two entries — a `-reality` link (if the node has
REALITY keys) and a `-tls` link (if it has a TLS domain). Clients that read
`Subscription-Userinfo` see the quota/expiry; `Profile-Update-Interval` hints a
12-hour refresh.

## Rotating credentials (on leak)

From a user's **Access** dialog (link icon):

- **Rotate sub link** — issues a new subscription token; the old link stops
  working immediately (the user must re-import).
- **Rotate UUID** — issues a new VLESS UUID and re-pushes config to the user's
  nodes, so the old UUID stops connecting at once.

## Traffic

- Usage is shown per user as `used / limit (percent)` with a bar. When the
  recent daily rate would exhaust the quota within two weeks, a **`~Nd to
  quota`** projection appears (estimated from the last 7 days' average).
- **Reset** (refresh icon) zeroes a user's counter immediately (e.g. a manual
  monthly reset). This clears both the aggregate and the per-node breakdown;
  daily history for the charts is preserved.
- **Stats** (chart icon) shows a 30-day daily-usage chart plus a **per-node**
  table with the uplink/downlink split for that user.

## Dashboard

The dashboard is a fleet overview rather than a single total:

- **Stat tiles** — nodes online, active users, **this month** (with today's
  usage) and **all-time traffic**, the last two split into uplink/downlink.
- **Fleet traffic** — a 30-day stacked daily chart (upload over download).
- **Traffic by node** — per-node up/down/total, plus a **live** throughput
  reading and the count of active clients (distinct source IPs in the last 5
  minutes). Click a row for that node's 30-day trend.
- **Top users by traffic** — per-user up/down/total.

Live throughput is a smoothed estimate updated from each node's traffic report
(every `STATS_INTERVAL`, default 60s); it also appears in the **Nodes** tab
health column, there split into uplink/downlink. That column also carries the
node's **agent build** — the commit its agent image was built from, recorded when
the agent last registered — so a node still running old code is visible without
reading the panel log. `dev` means a locally built agent, and an agent predating
the stamping reports its old hard-coded version instead. Per-node daily history begins
accruing when a node first reports under this version.

The **Nodes** tab health column also carries the host snapshot each agent sends
with its heartbeat — CPU, memory and disk usage, the active-client count, the
Xray version and the TLS cert countdown. Hover a reading for the detail behind
it: CPU model, core count and load average; memory and disk used out of total;
host uptime. Everything is read from the node's own `/proc` and its data mount,
so it needs no extra privileges — see
[PROTOCOL.md](PROTOCOL.md#node-metrics-source), which also explains why process
and connection counts are not shown. A node still running an older agent reports
only load and memory; upgrade it (`./deploy.sh node`) to fill in the rest.

Accounting is durable: the agent buffers reports and only clears them once the
panel acknowledges, and the panel de-duplicates by sequence number, so a dropped
connection or a restart on either side does not lose or double-count a window.
See [PROTOCOL.md](PROTOCOL.md).

## Devices (admin-only)

The **devices icon** opens the list of distinct client source IPs seen for the
user, derived from the Xray access log: source IP, connection type
(reality/tls), node, connection count, and last-seen. The user row shows a
device count for the last 30 days.

Only device-identifying metadata is recorded — **browsing destinations are never
stored**. Device information is **admin-only**; the self-service portal never
exposes it.

Records are retained for **30 days** after a device was last seen, then dropped
by a daily sweep; deleting the user or the node removes them immediately. So the
list is a rolling 30-day view, not a permanent history.

## Admin notes (private)

Each user has an **Admin note** field for a private remark (billing status,
contact, etc.). It is shown only in admin views and is **never** returned to the
portal or included in subscriptions.

## Telegram shortcuts

If the bot is configured you can also manage users from Telegram:
`/adduser <name> [limitGB] [days]`, `/deluser`, `/enable`, `/disable`,
`/traffic`, `/reset`, `/sub`, `/users`. See [telegram.md](telegram.md).
