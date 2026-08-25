#!/usr/bin/env bash
# Xuanwu (玄武) deploy dispatcher.
#
#   ./deploy.sh panel                 deploy the control panel
#   ./deploy.sh node                  deploy a panel-managed node (MODE=managed)
#   ./deploy.sh standalone            deploy a single node with no panel
#   ./deploy.sh user add <name>       (standalone) add a user, print share links
#   ./deploy.sh user rm <name>        (standalone) remove a user
#   ./deploy.sh user list             (standalone) list users
#   ./deploy.sh keys                  generate a REALITY x25519 keypair
#   ./deploy.sh reset-admin           reset the admin password from .env
#   ./deploy.sh backup [outfile]      copy the latest panel DB snapshot out
#   ./deploy.sh down  [panel|node]    stop a stack
#   ./deploy.sh logs  [panel|node]    follow logs
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
XRAY_IMAGE="${XRAY_IMAGE:-ghcr.io/xtls/xray-core:26.6.27}"

dc() { docker compose "$@"; }

ensure_env() {
	local dir="$1"
	if [[ ! -f "$dir/.env" ]]; then
		cp "$dir/.env.example" "$dir/.env"
		echo "Created $dir/.env from example. Edit it, then re-run this command." >&2
		exit 1
	fi
}

cmd_panel() {
	ensure_env "$ROOT/deploy/panel"
	mkdir -p "$ROOT/deploy/panel/data"
	( cd "$ROOT/deploy/panel" && dc up -d --build )
	echo "Panel is up. Open its public URL (set in the panel: Settings)."
}

# ensure_node_data scaffolds the node's runtime data dir (gitignored) with a
# minimal Xray config so xray boots before the agent pushes the real one.
ensure_node_data() {
	local d="$ROOT/deploy/node/data"
	mkdir -p "$d/xray" "$d/nginx" "$d/acme"
	[[ -s "$d/xray/config.json" ]] || cat > "$d/xray/config.json" <<'JSON'
{
  "api": {"services": ["StatsService", "HandlerService"], "tag": "api"},
  "stats": {},
  "inbounds": [
    {"tag": "api", "listen": "0.0.0.0", "port": 10085, "protocol": "dokodemo-door", "settings": {"address": "127.0.0.1"}}
  ],
  "outbounds": [{"protocol": "freedom", "tag": "direct"}],
  "routing": {"rules": [{"type": "field", "inboundTag": ["api"], "outboundTag": "api"}]}
}
JSON
}

cmd_node() {
	ensure_env "$ROOT/deploy/node"
	ensure_node_data
	( cd "$ROOT/deploy/node" && MODE=managed dc --env-file .env up -d --build )
	echo "Managed node is up; it will connect to the panel and receive its config."
}

cmd_standalone() {
	ensure_env "$ROOT/deploy/node"
	ensure_node_data
	( cd "$ROOT/deploy/node" && MODE=standalone dc --env-file .env up -d --build )
	echo "Standalone node is up. Add users with: ./deploy.sh user add <name>"
}

cmd_user() {
	( cd "$ROOT/deploy/node" && docker compose exec agent agent user "$@" )
}

cmd_keys() {
	docker run --rm "$XRAY_IMAGE" x25519
}

cmd_reset_admin() {
	ensure_env "$ROOT/deploy/panel"
	( cd "$ROOT/deploy/panel" && dc run --rm --build panel -reset-admin "$@" )
	echo "Done. Log in with PANEL_ADMIN_USER / PANEL_ADMIN_PASS from deploy/panel/.env."
}

cmd_down() {
	local which="${1:-}"
	case "$which" in
		panel) ( cd "$ROOT/deploy/panel" && dc down ) ;;
		node|standalone|"") ( cd "$ROOT/deploy/node" && dc down ) ;;
		*) echo "usage: ./deploy.sh down [panel|node]" >&2; exit 1 ;;
	esac
}

cmd_backup() {
	# Copy the latest scheduled snapshot out of the host-mounted panel data dir.
	local out="${1:-./panel-backup-$(date +%Y%m%d-%H%M%S).db}"
	local src
	src="$(ls -t "$ROOT"/deploy/panel/data/backups/panel-*.db 2>/dev/null | head -1)"
	if [[ -z "$src" ]]; then
		echo "No scheduled backup found yet (deploy/panel/data/backups/)." >&2
		exit 1
	fi
	cp "$src" "$out"
	echo "Copied $src -> $out"
}

cmd_logs() {
	local which="${1:-node}"
	case "$which" in
		panel) ( cd "$ROOT/deploy/panel" && dc logs -f ) ;;
		node|standalone) ( cd "$ROOT/deploy/node" && dc logs -f ) ;;
		*) echo "usage: ./deploy.sh logs [panel|node]" >&2; exit 1 ;;
	esac
}

main() {
	local cmd="${1:-}"; shift || true
	case "$cmd" in
		panel)       cmd_panel "$@" ;;
		node)        cmd_node "$@" ;;
		standalone)  cmd_standalone "$@" ;;
		user)        cmd_user "$@" ;;
		keys)        cmd_keys "$@" ;;
		reset-admin) cmd_reset_admin "$@" ;;
		backup)      cmd_backup "$@" ;;
		down)        cmd_down "$@" ;;
		logs)        cmd_logs "$@" ;;
		*)
			grep '^#' "$ROOT/deploy.sh" | sed 's/^# \{0,1\}//' | head -20
			exit 1 ;;
	esac
}

main "$@"
