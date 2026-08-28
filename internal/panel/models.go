package panel

// User is a proxy account. The username doubles as the Xray client "email",
// which is how traffic stats are attributed, so it must be unique.
type User struct {
	ID        int64   `json:"id"`
	Username  string  `json:"username"`
	UUID      string  `json:"uuid"`
	SubToken  string  `json:"sub_token"`
	DataLimit int64   `json:"data_limit"` // bytes; 0 = unlimited
	DataUsed  int64   `json:"data_used"`  // bytes, aggregate across nodes
	ExpireAt  int64   `json:"expire_at"`  // unix seconds; 0 = never
	Enabled   bool    `json:"enabled"`    // admin on/off switch
	CreatedAt int64   `json:"created_at"`
	NodeIDs   []int64 `json:"node_ids,omitempty"` // assigned nodes (read side)

	// PortalPasswordHash gates the self-service portal; never serialized.
	PortalPasswordHash string `json:"-"`
	// HasPortalPassword tells the admin UI whether a portal password is set.
	HasPortalPassword bool `json:"has_portal_password"`

	// ResetDay is the day of month (1-28) to auto-reset data_used; 0 disables.
	ResetDay int64 `json:"reset_day"`
	// LastReset is when the monthly counter was last auto-reset (unix seconds).
	LastReset int64 `json:"last_reset,omitempty"`
	// SessionEpoch bumps to revoke outstanding portal sessions; never serialized.
	SessionEpoch int64 `json:"-"`
	// MustChangePW forces a portal password change on next login.
	MustChangePW bool `json:"must_change_pw"`
	// DeviceCount is distinct source IPs seen recently; populated on list.
	DeviceCount int `json:"device_count"`
	// EtaDays projects days until the quota is hit at the recent daily rate;
	// 0 means unlimited, idle, or already over. Populated on list.
	EtaDays int64 `json:"eta_days,omitempty"`
	// Note is an admin-only remark. It is returned by admin endpoints only and
	// is never exposed to the portal or in subscriptions.
	Note string `json:"note"`
}

// active reports whether the user should currently be provisioned onto nodes.
func (u *User) active(now int64) bool {
	if !u.Enabled {
		return false
	}
	if u.ExpireAt > 0 && now >= u.ExpireAt {
		return false
	}
	if u.DataLimit > 0 && u.DataUsed >= u.DataLimit {
		return false
	}
	return true
}

// Node is one managed server running the Agent + nginx + Xray stack.
// Version identifies this panel build. Like the agent's, it is stamped at build
// time with the commit the image was built from (see Dockerfile.panel), so it
// cannot go stale the way a hand-maintained constant does.
var Version = "dev"

type Node struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Token     string `json:"token"`
	Address   string `json:"address"` // public host/IP used in subscription links
	Remark    string `json:"remark"`
	LastSeen  int64  `json:"last_seen"`
	Online    bool   `json:"online"`
	CreatedAt int64  `json:"created_at"`

	// REALITY parameters for this node's reality inbound.
	RealityDest       string `json:"reality_dest"`
	RealityServerName string `json:"reality_server_name"`
	RealityPrivateKey string `json:"reality_private_key"`
	RealityPublicKey  string `json:"reality_public_key"`
	RealityShortID    string `json:"reality_short_id"`
	// TLS domain for the TLS-Vision inbound (subscription SNI). May equal Address.
	TLSDomain string `json:"tls_domain"`
	// AgentVersion is what the agent reported when it last registered.
	AgentVersion string `json:"agent_version"`
}

// NodeTraffic is per-user per-node usage, kept for reporting/breakdown.
type NodeTraffic struct {
	UserID    int64 `json:"user_id"`
	NodeID    int64 `json:"node_id"`
	Up        int64 `json:"up"`
	Down      int64 `json:"down"`
	UpdatedAt int64 `json:"updated_at"`
}

// Admin is a panel operator.
type Admin struct {
	ID           int64
	Username     string
	PasswordHash string
	TOTPSecret   string
	TOTPEnabled  bool
	SessionEpoch int64
}
