package panel

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite database. A single process-wide write mutex keeps the
// (default) SQLite file happy under concurrent HTTP + websocket goroutines.
type Store struct {
	db    *sql.DB
	mu    sync.Mutex
	crypt *crypter // encrypts secret columns at rest; nil = plaintext (tests)
}

const schema = `
CREATE TABLE IF NOT EXISTS admins (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	username TEXT UNIQUE NOT NULL,
	password_hash TEXT NOT NULL,
	totp_secret TEXT NOT NULL DEFAULT '',
	totp_enabled INTEGER NOT NULL DEFAULT 0,
	session_epoch INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS nodes (
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
	traffic_seq INTEGER NOT NULL DEFAULT 0,
	agent_version TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS users (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	username TEXT UNIQUE NOT NULL,
	uuid TEXT NOT NULL,
	sub_token TEXT UNIQUE NOT NULL,
	data_limit INTEGER NOT NULL DEFAULT 0,
	data_used INTEGER NOT NULL DEFAULT 0,
	expire_at INTEGER NOT NULL DEFAULT 0,
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL DEFAULT 0,
	portal_password_hash TEXT NOT NULL DEFAULT '',
	reset_day INTEGER NOT NULL DEFAULT 0,
	last_reset INTEGER NOT NULL DEFAULT 0,
	session_epoch INTEGER NOT NULL DEFAULT 0,
	must_change_pw INTEGER NOT NULL DEFAULT 0,
	note TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS user_nodes (
	user_id INTEGER NOT NULL,
	node_id INTEGER NOT NULL,
	PRIMARY KEY (user_id, node_id)
);
CREATE TABLE IF NOT EXISTS node_traffic (
	user_id INTEGER NOT NULL,
	node_id INTEGER NOT NULL,
	up INTEGER NOT NULL DEFAULT 0,
	down INTEGER NOT NULL DEFAULT 0,
	updated_at INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (user_id, node_id)
);
CREATE TABLE IF NOT EXISTS traffic_daily (
	user_id INTEGER NOT NULL,
	day TEXT NOT NULL,               -- YYYY-MM-DD (UTC)
	up INTEGER NOT NULL DEFAULT 0,
	down INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (user_id, day)
);
CREATE TABLE IF NOT EXISTS node_traffic_daily (
	node_id INTEGER NOT NULL,
	day TEXT NOT NULL,               -- YYYY-MM-DD (UTC)
	up INTEGER NOT NULL DEFAULT 0,
	down INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (node_id, day)
);
CREATE TABLE IF NOT EXISTS audit_log (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	ts INTEGER NOT NULL,
	actor TEXT NOT NULL,
	action TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS user_devices (
	user_id INTEGER NOT NULL,
	node_id INTEGER NOT NULL,
	ip TEXT NOT NULL,
	inbound TEXT NOT NULL DEFAULT '',
	conns INTEGER NOT NULL DEFAULT 0,
	first_seen INTEGER NOT NULL DEFAULT 0,
	last_seen INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (user_id, node_id, ip)
);
`

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // serialize; simplest correct choice for SQLite
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}
	migrate(db)
	return &Store{db: db}, nil
}

// migrate applies idempotent, additive schema changes to databases created by an
// earlier version. ADD COLUMN errors (column already exists) are expected and
// ignored, so this is safe to run on every boot.
func migrate(db *sql.DB) {
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN portal_password_hash TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN reset_day INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN last_reset INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE nodes ADD COLUMN traffic_seq INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE admins ADD COLUMN totp_secret TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE admins ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE admins ADD COLUMN session_epoch INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN session_epoch INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN must_change_pw INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN note TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE nodes ADD COLUMN agent_version TEXT NOT NULL DEFAULT ''`)
}

// NodeTrafficSeq returns the last traffic batch sequence applied for a node.
func (s *Store) NodeTrafficSeq(nodeID int64) (int64, error) {
	var seq int64
	err := s.db.QueryRow(`SELECT traffic_seq FROM nodes WHERE id=?`, nodeID).Scan(&seq)
	return seq, err
}

// SetNodeTrafficSeq records the last applied traffic batch sequence for a node.
func (s *Store) SetNodeTrafficSeq(nodeID, seq int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE nodes SET traffic_seq=? WHERE id=?`, seq, nodeID)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

// GetSetting returns a settings value and whether the key exists.
func (s *Store) GetSetting(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// SetSetting upserts a settings value.
func (s *Store) SetSetting(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=?`, key, value, value)
	return err
}

// GetSecretSetting reads a settings value that is stored encrypted at rest (e.g.
// the Telegram bot token), returning its plaintext.
func (s *Store) GetSecretSetting(key string) (string, bool, error) {
	v, ok, err := s.GetSetting(key)
	if err != nil || !ok {
		return "", ok, err
	}
	return s.crypt.decrypt(v), true, nil
}

// SetSecretSetting upserts a settings value, encrypting it at rest.
func (s *Store) SetSecretSetting(key, value string) error {
	return s.SetSetting(key, s.crypt.encrypt(value))
}

// encryptExisting re-encrypts secret columns that were written before at-rest
// encryption was enabled (legacy plaintext). It is idempotent — values already
// carrying the encryption marker are skipped — and a no-op without a crypter.
func (s *Store) encryptExisting() error {
	if s.crypt == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reencryptColumn(`SELECT id, reality_private_key FROM nodes`,
		`UPDATE nodes SET reality_private_key=? WHERE id=?`); err != nil {
		return err
	}
	if err := s.reencryptColumn(`SELECT id, totp_secret FROM admins`,
		`UPDATE admins SET totp_secret=? WHERE id=?`); err != nil {
		return err
	}
	// settings.telegram_token is a single string-keyed row.
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key='telegram_token'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if v != "" && !isEncrypted(v) {
		_, err = s.db.Exec(`UPDATE settings SET value=? WHERE key='telegram_token'`, s.crypt.encrypt(v))
	}
	return err
}

// reencryptColumn encrypts every legacy-plaintext value returned by sel (which
// must select `id, value`) using upd (`... SET col=? WHERE id=?`). Rows are read
// fully before any write, since the single SQLite connection can't stream a query
// and execute updates at once. Caller holds s.mu.
func (s *Store) reencryptColumn(sel, upd string) error {
	rows, err := s.db.Query(sel)
	if err != nil {
		return err
	}
	type item struct {
		id  int64
		val string
	}
	var todo []item
	for rows.Next() {
		var id int64
		var v string
		if err := rows.Scan(&id, &v); err != nil {
			rows.Close()
			return err
		}
		if v != "" && !isEncrypted(v) {
			todo = append(todo, item{id, v})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, it := range todo {
		if _, err := s.db.Exec(upd, s.crypt.encrypt(it.val), it.id); err != nil {
			return err
		}
	}
	return nil
}

// BackupTo writes a consistent snapshot of the database to path (which must not
// already exist), using SQLite's VACUUM INTO.
func (s *Store) BackupTo(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`VACUUM INTO ?`, path)
	return err
}

// ---- admins ----

// EnsureAdmin creates the bootstrap admin on first boot only. Once the admin
// exists, the database is authoritative: a password changed in the panel is
// never overwritten by the (seed-only) PANEL_ADMIN_PASS on later restarts.
func (s *Store) EnsureAdmin(username, passwordHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var id int64
	err := s.db.QueryRow(`SELECT id FROM admins WHERE username=?`, username).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.Exec(`INSERT INTO admins(username,password_hash) VALUES(?,?)`, username, passwordHash)
		return err
	}
	return err // exists: leave the stored password (and hash) untouched
}

func (s *Store) GetAdmin(username string) (*Admin, error) {
	a := &Admin{}
	var enabled int
	err := s.db.QueryRow(`SELECT id,username,password_hash,totp_secret,totp_enabled,session_epoch FROM admins WHERE username=?`, username).
		Scan(&a.ID, &a.Username, &a.PasswordHash, &a.TOTPSecret, &enabled, &a.SessionEpoch)
	if err != nil {
		return nil, err
	}
	a.TOTPEnabled = enabled != 0
	a.TOTPSecret = s.crypt.decrypt(a.TOTPSecret)
	return a, nil
}

// BumpAdminSessionEpoch invalidates every existing admin session.
func (s *Store) BumpAdminSessionEpoch(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE admins SET session_epoch=session_epoch+1 WHERE username=?`, username)
	return err
}

// BumpUserSessionEpoch invalidates every existing portal session for a user.
func (s *Store) BumpUserSessionEpoch(userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE users SET session_epoch=session_epoch+1 WHERE id=?`, userID)
	return err
}

// SetAdminTOTP stores an admin's TOTP secret and enabled flag.
func (s *Store) SetAdminTOTP(username, secret string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := 0
	if enabled {
		e = 1
	}
	_, err := s.db.Exec(`UPDATE admins SET totp_secret=?,totp_enabled=? WHERE username=?`, s.crypt.encrypt(secret), e, username)
	return err
}

// ListAdmins returns all admin usernames.
func (s *Store) ListAdmins() ([]string, error) {
	rows, err := s.db.Query(`SELECT username FROM admins ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CountAdmins() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM admins`).Scan(&n)
	return n, err
}

func (s *Store) CreateAdmin(username, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO admins(username,password_hash) VALUES(?,?)`, username, hash)
	return err
}

func (s *Store) DeleteAdmin(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM admins WHERE username=?`, username)
	return err
}

// SetAdminPassword changes an admin's password and bumps its session epoch so
// existing sessions for that admin are revoked.
func (s *Store) SetAdminPassword(username, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE admins SET password_hash=?,session_epoch=session_epoch+1 WHERE username=?`, hash, username)
	return err
}

// ---- devices ----

// Device is one observed client device (source IP) for a user.
type Device struct {
	IP        string `json:"ip"`
	Inbound   string `json:"inbound"`
	Conns     int64  `json:"conns"`
	NodeID    int64  `json:"node_id"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
}

// UpsertDevice records/updates a user's device observation on a node.
func (s *Store) UpsertDevice(userID, nodeID int64, ip, inbound string, conns, seen int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO user_devices(user_id,node_id,ip,inbound,conns,first_seen,last_seen)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(user_id,node_id,ip) DO UPDATE SET conns=conns+?,inbound=?,last_seen=?`,
		userID, nodeID, ip, inbound, conns, seen, seen, conns, inbound, seen)
	return err
}

// ListUserDevices returns a user's devices, most-recent first.
func (s *Store) ListUserDevices(userID int64) ([]Device, error) {
	rows, err := s.db.Query(`SELECT ip,inbound,conns,node_id,first_seen,last_seen
		FROM user_devices WHERE user_id=? ORDER BY last_seen DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.IP, &d.Inbound, &d.Conns, &d.NodeID, &d.FirstSeen, &d.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeviceCounts returns distinct-IP device counts per user seen since `since`.
func (s *Store) DeviceCounts(since int64) (map[int64]int, error) {
	rows, err := s.db.Query(`SELECT user_id, COUNT(DISTINCT ip) FROM user_devices WHERE last_seen>=? GROUP BY user_id`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var uid int64
		var n int
		if err := rows.Scan(&uid, &n); err != nil {
			return nil, err
		}
		out[uid] = n
	}
	return out, rows.Err()
}

// NodeClientCounts returns the number of distinct client IPs seen per node since
// `since`, i.e. the clients currently active on each node.
func (s *Store) NodeClientCounts(since int64) (map[int64]int64, error) {
	rows, err := s.db.Query(`SELECT node_id, COUNT(DISTINCT ip) FROM user_devices WHERE last_seen>=? GROUP BY node_id`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var nid, n int64
		if err := rows.Scan(&nid, &n); err != nil {
			return nil, err
		}
		out[nid] = n
	}
	return out, rows.Err()
}

// PurgeDevices drops device observations last seen before cutoff and reports how
// many rows went. Device rows are only ever inserted, so without this the table
// grows for the life of the panel; nothing reads rows older than the 30-day
// window the UI reports on, so expiring them loses no displayed data.
func (s *Store) PurgeDevices(cutoff int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM user_devices WHERE last_seen<?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---- audit ----

// AuditEntry is one recorded admin action.
type AuditEntry struct {
	ID     int64  `json:"id"`
	TS     int64  `json:"ts"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Detail string `json:"detail"`
}

func (s *Store) AddAudit(actor, action, detail string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO audit_log(ts,actor,action,detail) VALUES(?,?,?,?)`,
		time.Now().Unix(), actor, action, detail)
	return err
}

func (s *Store) ListAudit(limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id,ts,actor,action,detail FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AuditEntry, 0, limit)
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.Action, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- nodes ----

func (s *Store) scanNode(sc interface{ Scan(...any) error }) (*Node, error) {
	n := &Node{}
	err := sc.Scan(&n.ID, &n.Name, &n.Token, &n.Address, &n.Remark, &n.LastSeen, &n.CreatedAt,
		&n.RealityDest, &n.RealityServerName, &n.RealityPrivateKey, &n.RealityPublicKey,
		&n.RealityShortID, &n.TLSDomain, &n.AgentVersion)
	if err != nil {
		return nil, err
	}
	n.RealityPrivateKey = s.crypt.decrypt(n.RealityPrivateKey)
	n.Online = time.Now().Unix()-n.LastSeen < 60
	return n, nil
}

const nodeCols = `id,name,token,address,remark,last_seen,created_at,reality_dest,reality_server_name,reality_private_key,reality_public_key,reality_short_id,tls_domain,agent_version`

func (s *Store) ListNodes() ([]*Node, error) {
	rows, err := s.db.Query(`SELECT ` + nodeCols + ` FROM nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Node
	for rows.Next() {
		n, err := s.scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) GetNode(id int64) (*Node, error) {
	return s.scanNode(s.db.QueryRow(`SELECT `+nodeCols+` FROM nodes WHERE id=?`, id))
}

func (s *Store) GetNodeByToken(token string) (*Node, error) {
	return s.scanNode(s.db.QueryRow(`SELECT `+nodeCols+` FROM nodes WHERE token=?`, token))
}

func (s *Store) CreateNode(n *Node) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`INSERT INTO nodes(name,token,address,remark,created_at,reality_dest,reality_server_name,reality_private_key,reality_public_key,reality_short_id,tls_domain)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		n.Name, n.Token, n.Address, n.Remark, time.Now().Unix(),
		n.RealityDest, n.RealityServerName, s.crypt.encrypt(n.RealityPrivateKey), n.RealityPublicKey, n.RealityShortID, n.TLSDomain)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateNode(n *Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE nodes SET name=?,address=?,remark=?,reality_dest=?,reality_server_name=?,reality_private_key=?,reality_public_key=?,reality_short_id=?,tls_domain=? WHERE id=?`,
		n.Name, n.Address, n.Remark, n.RealityDest, n.RealityServerName, s.crypt.encrypt(n.RealityPrivateKey), n.RealityPublicKey, n.RealityShortID, n.TLSDomain, n.ID)
	return err
}

func (s *Store) DeleteNode(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM user_nodes WHERE node_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM node_traffic WHERE node_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM node_traffic_daily WHERE node_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM user_devices WHERE node_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM nodes WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetNodeAgentVersion records the build a node's agent reported on register.
func (s *Store) SetNodeAgentVersion(id int64, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE nodes SET agent_version=? WHERE id=?`, v, id)
	return err
}

func (s *Store) TouchNode(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE nodes SET last_seen=? WHERE id=?`, time.Now().Unix(), id)
	return err
}

// ---- users ----

func scanUser(sc interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	var enabled int
	var mustChange int
	err := sc.Scan(&u.ID, &u.Username, &u.UUID, &u.SubToken, &u.DataLimit, &u.DataUsed, &u.ExpireAt, &enabled, &u.CreatedAt, &u.PortalPasswordHash, &u.ResetDay, &u.LastReset, &u.SessionEpoch, &mustChange, &u.Note)
	if err != nil {
		return nil, err
	}
	u.Enabled = enabled != 0
	u.HasPortalPassword = u.PortalPasswordHash != ""
	u.MustChangePW = mustChange != 0
	return u, nil
}

const userCols = `id,username,uuid,sub_token,data_limit,data_used,expire_at,enabled,created_at,portal_password_hash,reset_day,last_reset,session_epoch,must_change_pw,note`

func (s *Store) ListUsers() ([]*User, error) {
	rows, err := s.db.Query(`SELECT ` + userCols + ` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// attach node assignments
	for _, u := range out {
		ids, err := s.userNodeIDs(u.ID)
		if err != nil {
			return nil, err
		}
		u.NodeIDs = ids
	}
	return out, nil
}

func (s *Store) GetUser(id int64) (*User, error) {
	u, err := scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	u.NodeIDs, err = s.userNodeIDs(id)
	return u, err
}

func (s *Store) GetUserBySubToken(token string) (*User, error) {
	u, err := scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE sub_token=?`, token))
	if err != nil {
		return nil, err
	}
	u.NodeIDs, err = s.userNodeIDs(u.ID)
	return u, err
}

func (s *Store) GetUserByName(name string) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE username=?`, name))
}

func (s *Store) userNodeIDs(userID int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT node_id FROM user_nodes WHERE user_id=? ORDER BY node_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) CreateUser(u *User) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	enabled := 0
	if u.Enabled {
		enabled = 1
	}
	res, err := s.db.Exec(`INSERT INTO users(username,uuid,sub_token,data_limit,expire_at,enabled,created_at,reset_day,note)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		u.Username, u.UUID, u.SubToken, u.DataLimit, u.ExpireAt, enabled, time.Now().Unix(), u.ResetDay, u.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateUser(u *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	enabled := 0
	if u.Enabled {
		enabled = 1
	}
	_, err := s.db.Exec(`UPDATE users SET data_limit=?,expire_at=?,enabled=?,reset_day=?,note=? WHERE id=?`,
		u.DataLimit, u.ExpireAt, enabled, u.ResetDay, u.Note, u.ID)
	return err
}

// SetUserSubToken replaces a user's subscription token (rotation on leak).
func (s *Store) SetUserSubToken(userID int64, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE users SET sub_token=? WHERE id=?`, token, userID)
	return err
}

// SetUserUUID replaces a user's VLESS UUID (rotation on leak); requires a config
// re-push to every assigned node.
func (s *Store) SetUserUUID(userID int64, uuid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE users SET uuid=? WHERE id=?`, uuid, userID)
	return err
}

// SetUserLastReset records when a user's monthly counter was last auto-reset.
func (s *Store) SetUserLastReset(userID, ts int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE users SET last_reset=? WHERE id=?`, ts, userID)
	return err
}

// DailyTraffic is one day's aggregate usage for a user.
type DailyTraffic struct {
	Day  string `json:"day"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
}

// TrafficHistory returns the last `days` days of daily usage for a user, oldest
// first, including zero-fill for days with no traffic.
func (s *Store) TrafficHistory(userID int64, days int) ([]DailyTraffic, error) {
	if days < 1 {
		days = 30
	}
	rows, err := s.db.Query(`SELECT day,up,down FROM traffic_daily WHERE user_id=? AND day>=? ORDER BY day`,
		userID, time.Now().UTC().AddDate(0, 0, -days+1).Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := map[string]DailyTraffic{}
	for rows.Next() {
		var d DailyTraffic
		if err := rows.Scan(&d.Day, &d.Up, &d.Down); err != nil {
			return nil, err
		}
		byDay[d.Day] = d
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return fillDaily(byDay, days), nil
}

// fillDaily turns a day->usage map into an oldest-first series of length days,
// zero-filling days with no traffic so charts have a continuous x-axis.
func fillDaily(byDay map[string]DailyTraffic, days int) []DailyTraffic {
	out := make([]DailyTraffic, 0, days)
	for i := days - 1; i >= 0; i-- {
		day := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		if d, ok := byDay[day]; ok {
			out = append(out, d)
		} else {
			out = append(out, DailyTraffic{Day: day})
		}
	}
	return out
}

// FleetDaily returns fleet-wide daily usage (summed across all users) for the
// last `days` days, oldest first and zero-filled.
func (s *Store) FleetDaily(days int) ([]DailyTraffic, error) {
	if days < 1 {
		days = 30
	}
	since := time.Now().UTC().AddDate(0, 0, -days+1).Format("2006-01-02")
	rows, err := s.db.Query(`SELECT day, SUM(up), SUM(down) FROM traffic_daily WHERE day>=? GROUP BY day`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := map[string]DailyTraffic{}
	for rows.Next() {
		var d DailyTraffic
		if err := rows.Scan(&d.Day, &d.Up, &d.Down); err != nil {
			return nil, err
		}
		byDay[d.Day] = d
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return fillDaily(byDay, days), nil
}

// NodeDaily returns one node's daily usage for the last `days` days. Per-node
// daily accounting starts when this feature ships, so older days read as zero.
func (s *Store) NodeDaily(nodeID, days int64) ([]DailyTraffic, error) {
	if days < 1 {
		days = 30
	}
	since := time.Now().UTC().AddDate(0, 0, int(-days+1)).Format("2006-01-02")
	rows, err := s.db.Query(`SELECT day, up, down FROM node_traffic_daily WHERE node_id=? AND day>=? ORDER BY day`, nodeID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := map[string]DailyTraffic{}
	for rows.Next() {
		var d DailyTraffic
		if err := rows.Scan(&d.Day, &d.Up, &d.Down); err != nil {
			return nil, err
		}
		byDay[d.Day] = d
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return fillDaily(byDay, int(days)), nil
}

// PeriodStats holds today's and this calendar month's fleet usage (UTC).
type PeriodStats struct {
	Today TrafficSplit `json:"today"`
	Month TrafficSplit `json:"month"`
}

// PeriodTotals returns fleet usage for today and for the current month to date.
func (s *Store) PeriodTotals() (PeriodStats, error) {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	var ps PeriodStats
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(up),0),COALESCE(SUM(down),0) FROM traffic_daily WHERE day=?`, today).
		Scan(&ps.Today.Up, &ps.Today.Down); err != nil {
		return ps, err
	}
	err := s.db.QueryRow(`SELECT COALESCE(SUM(up),0),COALESCE(SUM(down),0) FROM traffic_daily WHERE day>=?`, monthStart).
		Scan(&ps.Month.Up, &ps.Month.Down)
	return ps, err
}

// RecentDailyAvg returns each user's average bytes/day over the last `days`
// days, used to project when a user will hit their quota.
func (s *Store) RecentDailyAvg(days int) (map[int64]int64, error) {
	if days < 1 {
		days = 7
	}
	since := time.Now().UTC().AddDate(0, 0, -days+1).Format("2006-01-02")
	rows, err := s.db.Query(`SELECT user_id, SUM(up+down) FROM traffic_daily WHERE day>=? GROUP BY user_id`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var uid, total int64
		if err := rows.Scan(&uid, &total); err != nil {
			return nil, err
		}
		out[uid] = total / int64(days)
	}
	return out, rows.Err()
}

// TrafficSplit is an uplink/downlink byte pair.
type TrafficSplit struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// NodeUsage aggregates current traffic on one node across all its users.
type NodeUsage struct {
	NodeID int64 `json:"node_id"`
	Up     int64 `json:"up"`
	Down   int64 `json:"down"`
	Users  int64 `json:"users"`
}

// NodeTrafficTotals returns per-node uplink/downlink totals and the number of
// users that generated traffic on each node.
func (s *Store) NodeTrafficTotals() ([]NodeUsage, error) {
	rows, err := s.db.Query(`SELECT node_id, SUM(up), SUM(down), COUNT(DISTINCT user_id)
		FROM node_traffic GROUP BY node_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeUsage
	for rows.Next() {
		var n NodeUsage
		if err := rows.Scan(&n.NodeID, &n.Up, &n.Down, &n.Users); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// UserTrafficTotals returns each user's current uplink/downlink split, summed
// across the nodes they use.
func (s *Store) UserTrafficTotals() (map[int64]TrafficSplit, error) {
	rows, err := s.db.Query(`SELECT user_id, SUM(up), SUM(down) FROM node_traffic GROUP BY user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]TrafficSplit{}
	for rows.Next() {
		var uid int64
		var t TrafficSplit
		if err := rows.Scan(&uid, &t.Up, &t.Down); err != nil {
			return nil, err
		}
		out[uid] = t
	}
	return out, rows.Err()
}

// UserTrafficByNode returns one user's current usage split by node, busiest first.
func (s *Store) UserTrafficByNode(userID int64) ([]NodeUsage, error) {
	rows, err := s.db.Query(`SELECT node_id, up, down FROM node_traffic
		WHERE user_id=? AND (up+down) > 0 ORDER BY (up+down) DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeUsage
	for rows.Next() {
		var n NodeUsage
		if err := rows.Scan(&n.NodeID, &n.Up, &n.Down); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) DeleteUser(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM user_nodes WHERE user_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM node_traffic WHERE user_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM user_devices WHERE user_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetUserNodes replaces a user's node assignments.
func (s *Store) SetUserNodes(userID int64, nodeIDs []int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM user_nodes WHERE user_id=?`, userID); err != nil {
		return err
	}
	for _, nid := range nodeIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO user_nodes(user_id,node_id) VALUES(?,?)`, userID, nid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UsersForNode returns all users assigned to a node.
func (s *Store) UsersForNode(nodeID int64) ([]*User, error) {
	rows, err := s.db.Query(`SELECT `+userCols+` FROM users u
		JOIN user_nodes un ON un.user_id=u.id WHERE un.node_id=? ORDER BY u.id`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// AddTraffic increments a user's aggregate usage and the per-node breakdown.
func (s *Store) AddTraffic(userID, nodeID, up, down int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE users SET data_used=data_used+? WHERE id=?`, up+down, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO node_traffic(user_id,node_id,up,down,updated_at) VALUES(?,?,?,?,?)
		ON CONFLICT(user_id,node_id) DO UPDATE SET up=up+?,down=down+?,updated_at=?`,
		userID, nodeID, up, down, now, up, down, now); err != nil {
		return err
	}
	day := time.Unix(now, 0).UTC().Format("2006-01-02")
	if _, err := tx.Exec(`INSERT INTO traffic_daily(user_id,day,up,down) VALUES(?,?,?,?)
		ON CONFLICT(user_id,day) DO UPDATE SET up=up+?,down=down+?`,
		userID, day, up, down, up, down); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO node_traffic_daily(node_id,day,up,down) VALUES(?,?,?,?)
		ON CONFLICT(node_id,day) DO UPDATE SET up=up+?,down=down+?`,
		nodeID, day, up, down, up, down); err != nil {
		return err
	}
	return tx.Commit()
}

// SetUserPortalPassword stores (or clears, when hash is "") a user's self-service
// portal password hash. mustChange marks the password as temporary so the portal
// forces the user to change it on next login.
func (s *Store) SetUserPortalPassword(userID int64, hash string, mustChange bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	mc := 0
	if mustChange {
		mc = 1
	}
	_, err := s.db.Exec(`UPDATE users SET portal_password_hash=?,must_change_pw=? WHERE id=?`, hash, mc, userID)
	return err
}

// ChangeUserPortalPassword sets a user's own new password, clears the
// must-change flag, and bumps the session epoch (revoking other sessions).
func (s *Store) ChangeUserPortalPassword(userID int64, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE users SET portal_password_hash=?,must_change_pw=0,session_epoch=session_epoch+1 WHERE id=?`, hash, userID)
	return err
}

// ResetUserTraffic zeroes a user's aggregate usage and per-node breakdown (e.g.
// monthly reset), keeping the two consistent. Daily history is preserved.
func (s *Store) ResetUserTraffic(userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE users SET data_used=0 WHERE id=?`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM node_traffic WHERE user_id=?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}
