package panel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"time"

	"xuanwu/internal/wire"
)

// ingestTraffic maps reported per-email increments to users and accumulates
// usage. It does not push config; the enforcement loop reconciles provisioning.
//
// Reports are only accepted for users actually assigned to the reporting node.
// This prevents a compromised or misbehaving node from inflating the usage of
// users it doesn't serve (which could force them over quota and disable them).
//
// seq dedups batches: the agent resends an unacked batch, so we apply each seq at
// most once. A seq that equals the last applied one is a duplicate (skip); a seq
// lower than the last applied means the agent restarted with a fresh buffer, so
// we accept it and re-baseline.
func (a *App) ingestTraffic(nodeID, seq int64, items []wire.TrafficItem) {
	last, err := a.store.NodeTrafficSeq(nodeID)
	if err != nil {
		log.Printf("ingest traffic: seq for node %d: %v", nodeID, err)
		return
	}
	if seq != 0 && seq == last {
		return // duplicate resend of an already-applied batch
	}
	users, err := a.store.UsersForNode(nodeID)
	if err != nil {
		log.Printf("ingest traffic: users for node %d: %v", nodeID, err)
		return
	}
	allowed := make(map[string]int64, len(users))
	for _, u := range users {
		allowed[u.Username] = u.ID
	}
	var batchUp, batchDown int64
	for _, it := range items {
		if it.Up == 0 && it.Down == 0 {
			continue
		}
		uid, ok := allowed[it.Email]
		if !ok {
			continue // node reported a user it isn't assigned; ignored
		}
		if err := a.store.AddTraffic(uid, nodeID, it.Up, it.Down); err != nil {
			log.Printf("add traffic %s: %v", it.Email, err)
		}
		batchUp += it.Up
		batchDown += it.Down
	}
	a.recordRate(nodeID, batchUp, batchDown)
	if seq != 0 {
		if err := a.store.SetNodeTrafficSeq(nodeID, seq); err != nil {
			log.Printf("set traffic seq node %d: %v", nodeID, err)
		}
	}
	// A big jump can push a user over quota; reconcile this node promptly.
	a.syncNode(nodeID)
}

// ingestDevices records client-device observations reported by a node, scoped to
// the users actually assigned to that node (same trust boundary as traffic).
func (a *App) ingestDevices(nodeID int64, items []wire.DeviceItem) {
	users, err := a.store.UsersForNode(nodeID)
	if err != nil {
		return
	}
	allowed := make(map[string]int64, len(users))
	for _, u := range users {
		allowed[u.Username] = u.ID
	}
	for _, it := range items {
		uid, ok := allowed[it.Email]
		if !ok || it.IP == "" {
			continue
		}
		seen := it.LastSeen
		if seen == 0 {
			seen = time.Now().Unix()
		}
		if err := a.store.UpsertDevice(uid, nodeID, it.IP, it.Inbound, it.Conns, seen); err != nil {
			log.Printf("upsert device %s/%s: %v", it.Email, it.IP, err)
		}
	}
}

// deviceRetention bounds how long device observations are kept. It matches the
// widest window anything reads them over (the 30-day distinct-device count on the
// users list), so nothing displayed changes.
const deviceRetention = 30 * 24 * time.Hour

// deviceCleanupLoop expires stale device observations once a day.
func (a *App) deviceCleanupLoop(ctx context.Context) {
	purge := func() {
		n, err := a.store.PurgeDevices(time.Now().Add(-deviceRetention).Unix())
		if err != nil {
			log.Printf("purge devices: %v", err)
			return
		}
		if n > 0 {
			log.Printf("purged %d device records older than %d days", n, int(deviceRetention.Hours()/24))
		}
	}
	purge()
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			purge()
		}
	}
}

// activeSignature is a stable hash of the users a node should currently serve.
// Changing membership, UUIDs or active state changes the signature.
func activeSignature(users []*User) string {
	now := time.Now().Unix()
	var lines []string
	for _, u := range users {
		if u.active(now) {
			lines = append(lines, u.Username+":"+u.UUID)
		}
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(fmt.Sprint(lines)))
	return hex.EncodeToString(sum[:])
}

// syncNode pushes a fresh config to the node only when its effective user set
// changed since the last push, avoiding needless Xray restarts.
//
// The signature is recorded only once the push actually reached the node's write
// queue. Recording it earlier would let a dropped push (node offline, queue
// backed up) look delivered, leaving the node serving a stale user set until it
// happened to reconnect — a removed user would keep working in the meantime.
func (a *App) syncNode(nodeID int64) {
	users, err := a.store.UsersForNode(nodeID)
	if err != nil {
		return
	}
	sig := activeSignature(users)
	a.activeMu.Lock()
	prev, ok := a.activeCache[nodeID]
	a.activeMu.Unlock()
	if ok && prev == sig {
		return
	}
	if !a.hub.PushConfig(nodeID) {
		return // not delivered; the stale signature stays, so the next sync retries
	}
	a.activeMu.Lock()
	a.activeCache[nodeID] = sig
	a.activeMu.Unlock()
}

// forceSyncNode always re-pushes (used after node settings change, e.g. REALITY
// keys, where the signature may be unchanged but the config differs, and on
// connect to seed the cache).
func (a *App) forceSyncNode(nodeID int64) {
	users, err := a.store.UsersForNode(nodeID)
	if err != nil {
		return
	}
	sig := activeSignature(users)
	if !a.hub.PushConfig(nodeID) {
		// Forget the signature: the node did not get this config, and the user
		// set alone may be unchanged, so syncNode would otherwise never retry.
		a.activeMu.Lock()
		delete(a.activeCache, nodeID)
		a.activeMu.Unlock()
		return
	}
	a.activeMu.Lock()
	a.activeCache[nodeID] = sig
	a.activeMu.Unlock()
}

// enforcementLoop periodically reconciles every online node so time-based
// expiry takes effect even without traffic.
func (a *App) enforcementLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for id := range a.hub.OnlineNodeIDs() {
				a.syncNode(id)
			}
			a.checkUserTransitions()
		}
	}
}

// monthlyResetDue reports whether a user's data counter should be reset now,
// given its reset day-of-month and when it was last reset. A reset fires once
// per month, on or after resetDay (clamped to the month's length).
func monthlyResetDue(now time.Time, resetDay int64, lastReset int64) (time.Time, bool) {
	if resetDay < 1 {
		return time.Time{}, false
	}
	y, m, _ := now.Date()
	daysInMonth := time.Date(y, m+1, 1, 0, 0, 0, 0, now.Location()).AddDate(0, 0, -1).Day()
	day := int(resetDay)
	if day > daysInMonth {
		day = daysInMonth
	}
	resetInstant := time.Date(y, m, day, 0, 0, 0, 0, now.Location())
	if now.Before(resetInstant) || lastReset >= resetInstant.Unix() {
		return time.Time{}, false
	}
	return resetInstant, true
}

// autoResetLoop applies monthly quota resets. It checks hourly so a reset lands
// within the hour after its scheduled day rolls over (UTC).
func (a *App) autoResetLoop(ctx context.Context) {
	check := func() {
		users, err := a.store.ListUsers()
		if err != nil {
			return
		}
		now := time.Now().UTC()
		for _, u := range users {
			if _, due := monthlyResetDue(now, u.ResetDay, u.LastReset); !due {
				continue
			}
			if err := a.store.ResetUserTraffic(u.ID); err != nil {
				log.Printf("auto-reset user %d: %v", u.ID, err)
				continue
			}
			_ = a.store.SetUserLastReset(u.ID, now.Unix())
			log.Printf("auto-reset traffic for user %s (day %d)", u.Username, u.ResetDay)
			for _, nid := range u.NodeIDs {
				a.syncNode(nid) // re-enable if it had been over quota
			}
		}
	}
	check()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			check()
		}
	}
}
