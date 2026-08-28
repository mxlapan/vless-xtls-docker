package panel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

// ---- auth ----

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct{ Username, Password, Code string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	ipKey := "ip:" + clientIP(r)
	userKey := "user:" + body.Username
	if a.loginLimiter.blocked(ipKey) || a.loginLimiter.blocked(userKey) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts; try again later")
		return
	}
	admin, err := a.store.GetAdmin(body.Username)
	if err != nil || !checkPassword(admin.PasswordHash, body.Password) {
		a.loginLimiter.fail(ipKey)
		a.loginLimiter.fail(userKey)
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if admin.TOTPEnabled {
		if body.Code == "" {
			// Signal the client to collect a 2FA code (password was correct).
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "2fa required", "totp_required": true})
			return
		}
		if !totpVerify(admin.TOTPSecret, body.Code) {
			a.loginLimiter.fail(ipKey)
			a.loginLimiter.fail(userKey)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid 2fa code", "totp_required": true})
			return
		}
	}
	a.loginLimiter.reset(ipKey)
	a.loginLimiter.reset(userKey)
	_ = a.store.AddAudit(admin.Username, "login", clientIP(r))
	tok := signSession(a.cfg.JWTSecret, roleAdmin, admin.Username, admin.SessionEpoch, 24*time.Hour)
	http.SetCookie(w, &http.Cookie{
		Name: "xuanwu_session", Value: tok, Path: "/", HttpOnly: true,
		Secure: a.cookieSecure(), SameSite: http.SameSiteLaxMode, MaxAge: 86400,
	})
	writeJSON(w, http.StatusOK, map[string]string{"username": admin.Username})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: "xuanwu_session", Value: "", Path: "/", HttpOnly: true,
		Secure: a.cookieSecure(), SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie("xuanwu_session")
	_, name, _, _ := verifySession(a.cfg.JWTSecret, c.Value)
	writeJSON(w, http.StatusOK, map[string]string{"username": name, "version": Version})
}

// handleLogoutAll revokes every admin session (this one included) by bumping the
// account's session epoch.
func (a *App) handleLogoutAll(w http.ResponseWriter, r *http.Request) {
	adm, ok := a.currentAdmin(r)
	if !ok {
		writeErr(w, 401, "unauthorized")
		return
	}
	if err := a.store.BumpAdminSessionEpoch(adm.Username); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "xuanwu_session", Value: "", Path: "/", HttpOnly: true,
		Secure: a.cookieSecure(), SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- nodes ----

func (a *App) handleListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := a.store.ListNodes()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	online := a.hub.OnlineNodeIDs()
	clients, _ := a.store.NodeClientCounts(time.Now().Unix() - 300)
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		rate := a.getNodeRate(n.ID)
		out = append(out, map[string]any{
			"id": n.ID, "name": n.Name, "address": n.Address, "remark": n.Remark,
			"online": online[n.ID], "last_seen": n.LastSeen,
			"reality_dest": n.RealityDest, "reality_server_name": n.RealityServerName,
			"reality_public_key": n.RealityPublicKey, "reality_short_id": n.RealityShortID,
			"tls_domain":    n.TLSDomain,
			"agent_version": n.AgentVersion,
			"metrics":       a.getNodeMetrics(n.ID),
			"rate_bps":      rate.Total(),
			"rate_up_bps":   rate.Up,
			"rate_down_bps": rate.Down,
			"clients":       clients[n.ID],
		})
	}
	writeJSON(w, 200, out)
}

func (a *App) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	var n Node
	if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
		writeErr(w, 400, "bad body")
		return
	}
	if strings.TrimSpace(n.Name) == "" {
		writeErr(w, 400, "name required")
		return
	}
	if err := validateNode(&n); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	n.Token = randToken(24)
	// TLS-Vision is opt-in: an empty tls_domain means REALITY only (no cert).
	id, err := a.store.CreateNode(&n)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	n.ID = id
	a.audit(r, "node.create", n.Name)
	writeJSON(w, 201, n)
}

func (a *App) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	n, err := a.store.GetNode(id)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	// Preserve secrets the UI never receives back, so an edit that omits them
	// (the list API doesn't expose the private key) can't wipe them.
	oldToken := n.Token
	oldPriv := n.RealityPrivateKey
	if err := json.NewDecoder(r.Body).Decode(n); err != nil {
		writeErr(w, 400, "bad body")
		return
	}
	n.ID = id
	n.Token = oldToken
	if strings.TrimSpace(n.RealityPrivateKey) == "" {
		n.RealityPrivateKey = oldPriv
	}
	if err := validateNode(n); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := a.store.UpdateNode(n); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.forceSyncNode(id) // REALITY/domain change requires re-push
	writeJSON(w, 200, n)
}

func (a *App) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	if err := a.store.DeleteNode(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.audit(r, "node.delete", fmt.Sprintf("id=%d", id))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) handleNodeInstall(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	n, err := a.store.GetNode(id)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	// --pull always fetches the prebuilt agent image; Compose still falls back to
	// a local build if the registry is unreachable.
	cmd := fmt.Sprintf(
		"PANEL_URL=%s NODE_TOKEN=%s DOMAIN=%s docker compose up -d --pull always",
		a.publicURL(), n.Token, n.TLSDomain)
	writeJSON(w, 200, map[string]string{
		"token":       n.Token,
		"panel_url":   a.publicURL(),
		"compose_cmd": cmd,
	})
}

// ---- users ----

func (a *App) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.store.ListUsers()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// Attach recent device counts (distinct IPs in the last 30 days).
	if counts, err := a.store.DeviceCounts(time.Now().AddDate(0, 0, -30).Unix()); err == nil {
		for _, u := range users {
			u.DeviceCount = counts[u.ID]
		}
	}
	// Project days-to-quota from the last 7 days' average daily usage.
	if avg, err := a.store.RecentDailyAvg(7); err == nil {
		for _, u := range users {
			if a := avg[u.ID]; u.DataLimit > 0 && u.DataUsed < u.DataLimit && a > 0 {
				u.EtaDays = (u.DataLimit - u.DataUsed + a - 1) / a
			}
		}
	}
	writeJSON(w, 200, users)
}

// handleUserDevices lists a user's observed client devices.
func (a *App) handleUserDevices(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	devices, err := a.store.ListUserDevices(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, devices)
}

func (a *App) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username  string  `json:"username"`
		DataLimit int64   `json:"data_limit"`
		ExpireAt  int64   `json:"expire_at"`
		ResetDay  int64   `json:"reset_day"`
		Note      string  `json:"note"`
		Enabled   *bool   `json:"enabled"`
		NodeIDs   []int64 `json:"node_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad body")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if err := validateUsername(body.Username); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if body.ResetDay < 0 || body.ResetDay > 28 {
		writeErr(w, 400, "reset_day must be 0-28")
		return
	}
	u := &User{
		Username:  body.Username,
		UUID:      newUUID(),
		SubToken:  randToken(18),
		DataLimit: body.DataLimit,
		ExpireAt:  body.ExpireAt,
		ResetDay:  body.ResetDay,
		Note:      strings.TrimSpace(body.Note),
		Enabled:   body.Enabled == nil || *body.Enabled,
	}
	id, err := a.store.CreateUser(u)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	u.ID = id
	if len(body.NodeIDs) > 0 {
		_ = a.store.SetUserNodes(id, body.NodeIDs)
		for _, nid := range body.NodeIDs {
			a.syncNode(nid)
		}
	}
	u.NodeIDs = body.NodeIDs
	a.audit(r, "user.create", u.Username)
	writeJSON(w, 201, u)
}

func (a *App) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	u, err := a.store.GetUser(id)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	var body struct {
		DataLimit *int64  `json:"data_limit"`
		ExpireAt  *int64  `json:"expire_at"`
		ResetDay  *int64  `json:"reset_day"`
		Note      *string `json:"note"`
		Enabled   *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad body")
		return
	}
	if body.DataLimit != nil {
		u.DataLimit = *body.DataLimit
	}
	if body.ExpireAt != nil {
		u.ExpireAt = *body.ExpireAt
	}
	if body.ResetDay != nil {
		if *body.ResetDay < 0 || *body.ResetDay > 28 {
			writeErr(w, 400, "reset_day must be 0-28")
			return
		}
		u.ResetDay = *body.ResetDay
	}
	if body.Note != nil {
		u.Note = strings.TrimSpace(*body.Note)
	}
	if body.Enabled != nil {
		u.Enabled = *body.Enabled
	}
	if err := a.store.UpdateUser(u); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for _, nid := range u.NodeIDs {
		a.syncNode(nid)
	}
	writeJSON(w, 200, u)
}

func (a *App) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	u, err := a.store.GetUser(id)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	nodeIDs := u.NodeIDs
	if err := a.store.DeleteUser(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for _, nid := range nodeIDs {
		a.syncNode(nid)
	}
	a.audit(r, "user.delete", u.Username)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) handleSetUserNodes(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	u, err := a.store.GetUser(id)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	var body struct {
		NodeIDs []int64 `json:"node_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad body")
		return
	}
	old := u.NodeIDs
	if err := a.store.SetUserNodes(id, body.NodeIDs); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// sync both the nodes the user left and the ones joined
	touched := map[int64]bool{}
	for _, nid := range append(append([]int64{}, old...), body.NodeIDs...) {
		touched[nid] = true
	}
	for nid := range touched {
		a.syncNode(nid)
	}
	writeJSON(w, 200, map[string]any{"node_ids": body.NodeIDs})
}

func (a *App) handleResetUserTraffic(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	if err := a.store.ResetUserTraffic(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	u, err := a.store.GetUser(id)
	if err == nil {
		for _, nid := range u.NodeIDs {
			a.syncNode(nid) // re-enable if it had been over quota
		}
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleRotateSub issues a fresh subscription token, invalidating the old link.
func (a *App) handleRotateSub(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	if _, err := a.store.GetUser(id); err != nil {
		writeErr(w, 404, "not found")
		return
	}
	tok := randToken(18)
	if err := a.store.SetUserSubToken(id, tok); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.audit(r, "user.rotate_sub", fmt.Sprintf("id=%d", id))
	writeJSON(w, 200, map[string]string{
		"sub_token": tok,
		"sub_url":   fmt.Sprintf("%s/sub/%s", a.publicURL(), tok),
	})
}

// handleRotateUUID issues a fresh VLESS UUID and re-pushes config to the user's
// nodes, so the old UUID stops working immediately.
func (a *App) handleRotateUUID(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	u, err := a.store.GetUser(id)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	uuid := newUUID()
	if err := a.store.SetUserUUID(id, uuid); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for _, nid := range u.NodeIDs {
		a.syncNode(nid) // UUID is part of the active signature, so this re-pushes
	}
	a.audit(r, "user.rotate_uuid", u.Username)
	writeJSON(w, 200, map[string]string{"uuid": uuid})
}

// handleUserTrafficHistory returns a user's daily usage series for charting.
func (a *App) handleUserTrafficHistory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	days := 30
	if q := r.URL.Query().Get("days"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}
	hist, err := a.store.TrafficHistory(id, days)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, hist)
}

// handleStats returns fleet-wide traffic broken down by node and per user, with
// an uplink/downlink split — richer than the single aggregate figure. Node and
// user names are resolved client-side from the existing lists.
func (a *App) handleStats(w http.ResponseWriter, r *http.Request) {
	nodeAgg, err := a.store.NodeTrafficTotals()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	userAgg, err := a.store.UserTrafficTotals()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var total TrafficSplit
	for _, n := range nodeAgg {
		total.Up += n.Up
		total.Down += n.Down
	}
	period, err := a.store.PeriodTotals()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	days := 30
	if q := r.URL.Query().Get("days"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}
	trend, err := a.store.FleetDaily(days)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"total": total,
		"today": period.Today,
		"month": period.Month,
		"trend": trend,
		"nodes": nodeAgg,
		"users": userAgg,
	})
}

// handleNodeTrafficHistory returns a node's daily usage series for charting.
func (a *App) handleNodeTrafficHistory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	days := int64(30)
	if q := r.URL.Query().Get("days"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 365 {
			days = int64(n)
		}
	}
	hist, err := a.store.NodeDaily(id, days)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, hist)
}

// handleUserTrafficNodes returns one user's current usage split by node.
func (a *App) handleUserTrafficNodes(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	rows, err := a.store.UserTrafficByNode(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, rows)
}

// handleSetPortalPassword sets (or clears, when the password is empty) a user's
// self-service portal password.
func (a *App) handleSetPortalPassword(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	if _, err := a.store.GetUser(id); err != nil {
		writeErr(w, 404, "not found")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad body")
		return
	}
	pw := strings.TrimSpace(body.Password)
	if pw == "" {
		if err := a.store.SetUserPortalPassword(id, "", false); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		_ = a.store.BumpUserSessionEpoch(id) // kick any active portal sessions
		writeJSON(w, 200, map[string]any{"has_portal_password": false})
		return
	}
	if err := validatePasswordStrength(pw); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	hash, err := hashPassword(pw)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// Admin-set passwords are temporary: the user must change on first login.
	if err := a.store.SetUserPortalPassword(id, hash, true); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	_ = a.store.BumpUserSessionEpoch(id) // a password change revokes old sessions
	writeJSON(w, 200, map[string]any{"has_portal_password": true})
}

func (a *App) handleUserSubInfo(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	u, err := a.store.GetUser(id)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	writeJSON(w, 200, map[string]string{
		"sub_url": fmt.Sprintf("%s/sub/%s", a.publicURL(), u.SubToken),
	})
}
