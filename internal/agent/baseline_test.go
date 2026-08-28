package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// baselineFixture writes a config file and returns the users file path (whose
// directory holds the baseline) and the config path.
func baselineFixture(t *testing.T, config string) (usersFile, configPath string) {
	t.Helper()
	dir := t.TempDir()
	configPath = filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "users.json"), configPath
}

func TestBaselineSurvivesAgentRestart(t *testing.T) {
	usersFile, configPath := baselineFixture(t, `{"inbounds":[]}`)
	clients := map[string]map[string]string{"vless-reality": {"alice": "uuid-a"}}

	// An agent applies a config and confirms Xray is running it.
	s := newLiveState(baselinePath(usersFile))
	s.set("skeleton-v1", clients, fileSHA(configPath))

	// The agent is replaced (an update); a fresh one recovers the baseline.
	restarted := newLiveState(baselinePath(usersFile))
	restarted.seed(configPath)
	if restarted.skeleton != "skeleton-v1" {
		t.Fatalf("skeleton = %q, want skeleton-v1 (a restart would drop connections)", restarted.skeleton)
	}
	if got := restarted.clients["vless-reality"]["alice"]; got != "uuid-a" {
		t.Errorf("clients not recovered: %+v", restarted.clients)
	}
	if restarted.sha != fileSHA(configPath) {
		t.Error("recovered baseline lost its config hash")
	}

	// The baseline embeds the REALITY private key, so it must not be readable
	// beyond the owner.
	info, err := os.Stat(baselinePath(usersFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("baseline mode = %o, want 600", perm)
	}
}

func TestBaselineRejectedWhenConfigChanged(t *testing.T) {
	usersFile, configPath := baselineFixture(t, `{"inbounds":[]}`)
	s := newLiveState(baselinePath(usersFile))
	s.set("skeleton-v1", map[string]map[string]string{}, fileSHA(configPath))

	// Something rewrote config.json without us restarting Xray for it: the
	// running Xray no longer matches, so the baseline must not be trusted.
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[{"tag":"new"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	restarted := newLiveState(baselinePath(usersFile))
	restarted.seed(configPath)
	if restarted.skeleton != "" {
		t.Errorf("stale baseline was trusted: %q", restarted.skeleton)
	}
}

func TestBaselineAbsent(t *testing.T) {
	usersFile, configPath := baselineFixture(t, `{"inbounds":[]}`)
	s := newLiveState(baselinePath(usersFile))
	s.seed(configPath) // nothing recorded yet
	if s.skeleton != "" {
		t.Errorf("skeleton = %q, want empty", s.skeleton)
	}
	// A state with no path (tests, standalone edge cases) must not panic.
	newLiveState("").seed(configPath)
}

func TestLiveApplyWithoutBaselineRequestsRestart(t *testing.T) {
	// No baseline means the agent cannot know what Xray runs, so it must ask for
	// a restart rather than dial gRPC and guess.
	c := Config{live: newLiveState("")}
	if c.liveApply("some-skeleton", map[string]map[string]string{}, "sha") {
		t.Error("liveApply reported success without a baseline")
	}
}
