# Panel ↔ Agent protocol

Transport: a single **outbound** WebSocket from the Agent to the Panel at
`GET {PANEL_URL}/api/node/ws`. The Agent authenticates by sending a `register`
frame with its node token first. All frames are JSON objects with a `type`
field (see `internal/wire`).

## Agent → Panel

```jsonc
// first frame — authenticates the connection. version is the commit the agent
// image was built from, stamped in at build time; the Panel stores it and shows
// it on the node so a node left on an old build is visible. The Panel binary
// carries the same stamp, shown under its sidebar logo.
{ "type": "register", "token": "<node token>", "version": "a1b2c3d" }

// periodic liveness + node health (every ~20s)
{ "type": "heartbeat", "metrics": {
    "load_avg": 0.21, "mem_used_pct": 30, "mem_total": 12483125248, "mem_used": 3758096384,
    "cpu_used_pct": 17, "cpu_cores": 2, "cpu_model": "Neoverse-N1",
    "disk_total": 155989377024, "disk_used": 63501271040, "host_uptime": 2268000,
    "xray_version": "26.6.27", "cert_expiry": 1815317059, "uptime": 1972
} }

// traffic batch; seq correlates with the panel's ack (see Durability)
{ "type": "traffic", "seq": 42, "items": [
    { "email": "alice", "up": 12345, "down": 67890 }
] }

// client devices seen since the last report (source IP + inbound type only)
{ "type": "devices", "devices": [
    { "email": "alice", "ip": "203.0.113.5", "inbound": "reality",
      "conns": 2, "last_seen": 1783759770 }
] }
```

## Panel → Agent

```jsonc
// sent right after a successful register, and whenever the node's effective
// user set / settings change. Full Xray config, not a diff. tls_domain (optional)
// is the node's TLS domain; the Agent publishes it for the acme sidecar to issue
// a cert, so managed nodes never repeat it in .env.
{ "type": "config", "config": { /* full Xray config.json object */ }, "tls_domain": "node.example.com" }

// acknowledges a traffic batch by seq
{ "type": "ack", "seq": 42 }
```

## Reconnection

The Agent reconnects with exponential backoff (1s → 30s cap). On every
(re)connect it re-registers and the Panel re-pushes the current config, so the
node is eventually consistent with Panel state.

To avoid pointless Xray churn the Panel skips a push when the node's effective
user set is unchanged since the last one, keyed by a signature it caches per
node. That signature is recorded **only once the config has actually been handed
to the node's write queue** — a push to an offline node, or one dropped because
the queue is backed up, leaves the cache alone so the next sync retries. Were it
recorded up front, an undelivered removal would look applied and the node would
keep serving the removed user until it happened to reconnect.

## Traffic durability (exactly-once-ish)

Xray's stats are read **and reset** each interval, so once collected the bytes
exist only in the Agent until the Panel confirms them:

- The Agent keeps a **durable, disk-persisted buffer**. A batch is sent under a
  monotonically increasing `seq` and **resent until acked**; only then is it
  cleared. An Agent restart or a dropped connection therefore never loses a
  window.
- The Panel records the **last applied `seq` per node** and ignores duplicates
  (a resend equal to the last seq). A `seq` lower than the last applied means the
  Agent restarted with a fresh buffer, and the Panel re-baselines. This gives
  effectively exactly-once accounting across reconnects and restarts on either
  side.

## Config application (restart-free when possible)

The Agent writes the received config to the shared `config.json` volume, then:

- **Live path** — if only the user set changed, it applies the delta to the
  running Xray over its gRPC `HandlerService` (`AlterInbound` add/remove user) —
  **no restart, no dropped connections**.
- **Restart path** — if non-user settings changed (or the live path fails), it
  restarts the `xray` container via the docker proxy, then records the new
  baseline.

The baseline — the config with its client lists emptied, plus the users Xray
currently holds — is mirrored to `data/xray-baseline.json` alongside the SHA-256
of the config file it was applied from. On boot the agent restores it if that
hash still matches `config.json`, which it only can if Xray was actually started
with those bytes. That is what makes **agent updates seamless**: without it a
restarted agent knows nothing about the running Xray, so the panel's first push
after every update would take the restart path and drop every live connection. A
missing or mismatched record is simply ignored, costing only the restart that
would have happened anyway.

A separate watcher re-applies the config when the **TLS certificate file**
changes (renewals, or a first-time cert enabling TLS-Vision), independent of
config pushes.

## Traffic accounting source

Every `STATS_INTERVAL` seconds the Agent runs, inside the xray container:

```
xray api statsquery --server=127.0.0.1:10085 -pattern "user>>>" -reset
```

Stat names look like `user>>>alice>>>traffic>>>uplink`. The Agent groups by email
into the durable buffer. The Panel adds increments to each user's `data_used`
(scoped to users actually assigned to the reporting node), then enforces
`data_limit` / `expire_at` / enabled: over-quota, expired or disabled users are
dropped from generated configs and a fresh `config` frame is pushed.

## Node metrics source

Host readings come from procfs globals (`/proc/stat`, `/proc/meminfo`,
`/proc/uptime`, `/proc/cpuinfo`) plus `statfs` on the agent's `/data` bind mount.
None of those are namespaced, so they describe the **host** even though the agent
runs in an unprivileged container — no host pid/net namespace and no extra
mounts. (`xray_version` is the exception: it comes from the hardened docker
proxy's inspect, as it always has.)

Process and connection counts are deliberately **not** reported: `/proc/<pid>`
and `/proc/net/*` are namespaced, so a container can only see its own handful of
processes and sockets. Reporting them would need `pid: host` / `network_mode:
host`, which is a much larger blast radius than the numbers are worth.

`cpu_used_pct` is a delta between consecutive samples, so the first heartbeat
after an agent restart reports 0. Fields beyond `load_avg`, `mem_used_pct`,
`xray_version`, `cert_expiry` and `uptime` are `omitempty` and simply absent from
an agent older than the panel; the panel treats `cpu_cores`, `mem_total` and
`disk_total` as the "was this reported at all" markers, since none of them is
ever legitimately zero on a live host.

## Device tracking source

The Agent tails the Xray **access log** (`XRAY_ACCESS_LOG`), extracting per-user
**source IP** and **inbound type** (reality/tls) only — never the browsing
destination. The source IP is the **real client IP**: nginx sends the PROXY
protocol to xray (`proxy_protocol on;`) and the inbounds accept it
(`acceptProxyProtocol`), so xray logs the client rather than the nginx container.
Reports are scoped to the reporting node's assigned users. See
[users.md](users.md#devices-admin-only).

The read offset is persisted to `data/access-offset.json`, together with the log
file's inode so a rotation is still detected. Keeping it only in memory meant a
restarted agent re-read the whole log and re-reported every source IP it had ever
seen, which surfaced as a node claiming hundreds of active clients right after an
update. `last_seen` is taken from each line's own timestamp rather than the
collection time, so a backlog written while the agent was down is dated when it
actually happened; a timestamp ahead of the agent's clock (a zone mismatch) falls
back to now, since a device parked in the future would count as active forever.

Device rows are kept for **30 days** after they were last seen and expired by a
daily sweep on the Panel; nothing reads them over a wider window, so the sweep
costs no displayed data and stops the table growing for the life of the panel.
Deleting a user or a node drops its device rows with it.
