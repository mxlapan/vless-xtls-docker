package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// baseline is the durable copy of liveState: what Xray was last confirmed to be
// running. Without it a restarted agent knows nothing about the running Xray, so
// the panel's first config push after every agent update would fall back to
// restarting the container and dropping every live connection — an agent update
// could never be seamless.
//
// ConfigSHA is what makes trusting the file safe: it is only written once Xray
// has actually been (re)started with those exact bytes, so a baseline whose hash
// still matches the config file on disk describes the running Xray. Any mismatch
// is treated as "unknown" and costs nothing but the restart we would have done
// anyway.
type baseline struct {
	Skeleton  string                       `json:"skeleton"`
	Clients   map[string]map[string]string `json:"clients"`
	ConfigSHA string                       `json:"config_sha"`
}

func baselinePath(usersFile string) string {
	return filepath.Join(filepath.Dir(usersFile), "xray-baseline.json")
}

func bytesSHA(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func fileSHA(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return bytesSHA(b)
}

// seed restores the baseline recorded for configPath, letting a restarted agent
// keep applying user changes live instead of restarting Xray. A missing or stale
// record simply leaves the baseline empty.
func (s *liveState) seed(configPath string) {
	if s.path == "" || configPath == "" {
		return
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var bl baseline
	if err := json.Unmarshal(b, &bl); err != nil || bl.Skeleton == "" {
		return
	}
	if bl.ConfigSHA == "" || bl.ConfigSHA != fileSHA(configPath) {
		log.Printf("xray baseline does not match %s; the next config push will restart xray", configPath)
		return
	}
	if bl.Clients == nil {
		bl.Clients = map[string]map[string]string{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skeleton, s.clients, s.sha = bl.Skeleton, bl.Clients, bl.ConfigSHA
	log.Printf("recovered xray baseline; user changes apply live, no restart needed")
}

// persist writes the baseline to disk. The caller must hold s.mu. The file holds
// the same secrets as config.json (REALITY private key), hence 0600.
func (s *liveState) persist() {
	if s.path == "" || s.skeleton == "" {
		return
	}
	b, err := json.Marshal(baseline{Skeleton: s.skeleton, Clients: s.clients, ConfigSHA: s.sha})
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, s.path)
}
