package panel

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"xuanwu/internal/wire"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPortalPasswordFlag(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateUser(&User{Username: "a", UUID: "u", SubToken: "tok", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := s.GetUser(id)
	if u.HasPortalPassword {
		t.Fatal("new user should not have a portal password")
	}
	if err := s.SetUserPortalPassword(id, "somehash", false); err != nil {
		t.Fatal(err)
	}
	u, _ = s.GetUser(id)
	if !u.HasPortalPassword || u.PortalPasswordHash != "somehash" {
		t.Fatalf("portal password not stored: %+v", u)
	}
}

func TestMustChangePasswordFlow(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateUser(&User{Username: "a", UUID: "u", SubToken: "tok", Enabled: true})
	// Admin sets a temporary password.
	if err := s.SetUserPortalPassword(id, "hash1", true); err != nil {
		t.Fatal(err)
	}
	u, _ := s.GetUser(id)
	if !u.MustChangePW || !u.HasPortalPassword {
		t.Fatalf("expected must-change + has-password, got %+v", u)
	}
	epoch0 := u.SessionEpoch
	// User changes it themselves.
	if err := s.ChangeUserPortalPassword(id, "hash2"); err != nil {
		t.Fatal(err)
	}
	u, _ = s.GetUser(id)
	if u.MustChangePW {
		t.Fatal("must-change flag should be cleared")
	}
	if u.PortalPasswordHash != "hash2" {
		t.Fatalf("password not updated: %q", u.PortalPasswordHash)
	}
	if u.SessionEpoch != epoch0+1 {
		t.Fatalf("session epoch not bumped: %d -> %d", epoch0, u.SessionEpoch)
	}
}

func TestUserDevices(t *testing.T) {
	s := newTestStore(t)
	uid, _ := s.CreateUser(&User{Username: "a", UUID: "u", SubToken: "tok", Enabled: true})
	nid, _ := s.CreateNode(&Node{Name: "n", Token: "nt"})
	now := int64(1_800_000_000)
	// Same IP twice -> conns accumulate; two distinct IPs -> count 2.
	s.UpsertDevice(uid, nid, "1.2.3.4", "reality", 1, now)
	s.UpsertDevice(uid, nid, "1.2.3.4", "reality", 2, now+10)
	s.UpsertDevice(uid, nid, "5.6.7.8", "tls", 1, now+20)

	devs, err := s.ListUserDevices(uid)
	if err != nil || len(devs) != 2 {
		t.Fatalf("devices = %v (err %v), want 2", devs, err)
	}
	// Most recent first.
	if devs[0].IP != "5.6.7.8" {
		t.Fatalf("expected newest first, got %s", devs[0].IP)
	}
	var conns14 int64
	for _, d := range devs {
		if d.IP == "1.2.3.4" {
			conns14 = d.Conns
		}
	}
	if conns14 != 3 {
		t.Fatalf("1.2.3.4 conns = %d, want 3", conns14)
	}
	counts, err := s.DeviceCounts(now)
	if err != nil || counts[uid] != 2 {
		t.Fatalf("device count = %v (err %v), want 2", counts[uid], err)
	}
	// Cutoff after all activity -> 0.
	if c, _ := s.DeviceCounts(now + 1000); c[uid] != 0 {
		t.Fatalf("device count with future cutoff = %d, want 0", c[uid])
	}
}

func TestMigrationIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	s1, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()
	// Re-opening (migrate runs again) must not error.
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	s2.Close()
}

func TestBackupTo(t *testing.T) {
	s := newTestStore(t)
	s.CreateUser(&User{Username: "a", UUID: "u", SubToken: "s", Enabled: true})
	bpath := filepath.Join(t.TempDir(), "b.db")
	if err := s.BackupTo(bpath); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(bpath)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("backup missing or empty: %v", err)
	}
}

func TestIngestTrafficScopingAndDedup(t *testing.T) {
	s := newTestStore(t)
	app := &App{store: s, activeCache: map[int64]string{}}
	app.hub = NewHub(app)

	nid, _ := s.CreateNode(&Node{Name: "n", Token: "nt"})
	uid, _ := s.CreateUser(&User{Username: "assigned", UUID: "u1", SubToken: "s1", Enabled: true})
	if err := s.SetUserNodes(uid, []int64{nid}); err != nil {
		t.Fatal(err)
	}
	other, _ := s.CreateUser(&User{Username: "other", UUID: "u2", SubToken: "s2", Enabled: true}) // not assigned

	// Batch seq 1: reports for an assigned and an unassigned user.
	app.ingestTraffic(nid, 1, []wire.TrafficItem{
		{Email: "assigned", Up: 100, Down: 200},
		{Email: "other", Up: 999, Down: 999},
	})
	if ua, _ := s.GetUser(uid); ua.DataUsed != 300 {
		t.Fatalf("assigned used=%d want 300", ua.DataUsed)
	}
	if uo, _ := s.GetUser(other); uo.DataUsed != 0 {
		t.Fatalf("unassigned user got traffic: used=%d want 0", uo.DataUsed)
	}

	// Duplicate seq 1 must be ignored.
	app.ingestTraffic(nid, 1, []wire.TrafficItem{{Email: "assigned", Up: 100, Down: 200}})
	if ua, _ := s.GetUser(uid); ua.DataUsed != 300 {
		t.Fatalf("duplicate batch applied: used=%d want 300", ua.DataUsed)
	}

	// New seq 2 applies.
	app.ingestTraffic(nid, 2, []wire.TrafficItem{{Email: "assigned", Up: 0, Down: 50}})
	if ua, _ := s.GetUser(uid); ua.DataUsed != 350 {
		t.Fatalf("seq 2 not applied: used=%d want 350", ua.DataUsed)
	}
}

func TestPurgeDevices(t *testing.T) {
	s := newTestStore(t)
	uid, _ := s.CreateUser(&User{Username: "a", UUID: "u", SubToken: "tok", Enabled: true})
	nid, _ := s.CreateNode(&Node{Name: "n", Token: "nt"})
	now := int64(1_800_000_000)
	day := int64(24 * 3600)
	s.UpsertDevice(uid, nid, "1.1.1.1", "reality", 1, now-40*day)
	s.UpsertDevice(uid, nid, "2.2.2.2", "reality", 1, now-1*day)

	n, err := s.PurgeDevices(now - 30*day)
	if err != nil || n != 1 {
		t.Fatalf("purged %d (err %v), want 1", n, err)
	}
	devs, _ := s.ListUserDevices(uid)
	if len(devs) != 1 || devs[0].IP != "2.2.2.2" {
		t.Fatalf("devices after purge = %+v, want only 2.2.2.2", devs)
	}
	// Idempotent: a second run with the same cutoff removes nothing.
	if n, _ := s.PurgeDevices(now - 30*day); n != 0 {
		t.Fatalf("second purge removed %d rows, want 0", n)
	}
}

func TestDeleteCascadesDeviceRows(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateUser(&User{Username: "a", UUID: "u1", SubToken: "s1", Enabled: true})
	b, _ := s.CreateUser(&User{Username: "b", UUID: "u2", SubToken: "s2", Enabled: true})
	nid, _ := s.CreateNode(&Node{Name: "n", Token: "nt"})
	now := int64(1_800_000_000)
	s.UpsertDevice(a, nid, "1.1.1.1", "reality", 1, now)
	s.UpsertDevice(b, nid, "2.2.2.2", "reality", 1, now)

	if err := s.DeleteUser(a); err != nil {
		t.Fatal(err)
	}
	if devs, _ := s.ListUserDevices(a); len(devs) != 0 {
		t.Fatalf("deleted user kept %d device rows", len(devs))
	}
	if c, _ := s.NodeClientCounts(now - 1); c[nid] != 1 {
		t.Fatalf("node client count = %d, want 1", c[nid])
	}

	if err := s.DeleteNode(nid); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.NodeClientCounts(now - 1); c[nid] != 0 {
		t.Fatalf("deleted node kept %d device rows", c[nid])
	}
}

// TestMigrationAddsAgentVersion opens a database whose nodes table predates the
// agent_version column. Every node query selects nodeCols, so a migration that
// did not land would break the panel outright rather than degrade.
func TestMigrationAddsAgentVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE nodes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		token TEXT UNIQUE NOT NULL,
		address TEXT NOT NULL DEFAULT '',
		remark TEXT NOT NULL DEFAULT '',
		last_seen INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL DEFAULT 0,
		reality_dest TEXT NOT NULL DEFAULT '',
		reality_server_name TEXT NOT NULL DEFAULT '',
		reality_private_key TEXT NOT NULL DEFAULT '',
		reality_public_key TEXT NOT NULL DEFAULT '',
		reality_short_id TEXT NOT NULL DEFAULT '',
		tls_domain TEXT NOT NULL DEFAULT '',
		traffic_seq INTEGER NOT NULL DEFAULT 0
	);
	INSERT INTO nodes(name,token) VALUES('legacy','tok')`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("open pre-migration db: %v", err)
	}
	defer s.Close()

	nodes, err := s.ListNodes()
	if err != nil || len(nodes) != 1 {
		t.Fatalf("ListNodes = %v (err %v), want 1 node", nodes, err)
	}
	if nodes[0].AgentVersion != "" {
		t.Fatalf("migrated node reports version %q, want empty", nodes[0].AgentVersion)
	}
	if err := s.SetNodeAgentVersion(nodes[0].ID, "a1b2c3d"); err != nil {
		t.Fatal(err)
	}
	n, err := s.GetNode(nodes[0].ID)
	if err != nil || n.AgentVersion != "a1b2c3d" {
		t.Fatalf("version = %q (err %v), want a1b2c3d", n.AgentVersion, err)
	}
}
