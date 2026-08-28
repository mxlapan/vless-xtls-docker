package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAccessWatcherParse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	w := newAccessWatcher(path, filepath.Join(filepath.Dir(path), "access-offset.json"))
	w.collect() // establish the read position on the (empty) log
	lines := "" +
		"2026/07/12 09:00:00.123 from 1.2.3.4:5678 accepted tcp:example.com:443 [vless-reality-vision -> direct] email: alice\n" +
		"2026/07/12 09:00:01.000 from 1.2.3.4:5679 accepted tcp:foo.com:443 [vless-reality-vision -> direct] email: alice\n" +
		"2026/07/12 09:00:02.000 from 5.6.7.8:1234 accepted tcp:bar.com:443 [vless-xtls-vision -> direct] email: bob\n"
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	got := w.collect()
	byKey := map[string]struct {
		inbound string
		conns   int64
	}{}
	for _, d := range got {
		byKey[d.Email+"|"+d.IP] = struct {
			inbound string
			conns   int64
		}{d.Inbound, d.Conns}
	}
	if v := byKey["alice|1.2.3.4"]; v.conns != 2 || v.inbound != "reality" {
		t.Fatalf("alice device = %+v, want conns=2 inbound=reality", v)
	}
	if v := byKey["bob|5.6.7.8"]; v.conns != 1 || v.inbound != "tls" {
		t.Fatalf("bob device = %+v, want conns=1 inbound=tls", v)
	}

	// Nothing new on a second call.
	if again := w.collect(); len(again) != 0 {
		t.Fatalf("expected no new devices, got %d", len(again))
	}

	// Appended line is picked up incrementally.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("2026/07/12 09:05:00.000 from 9.9.9.9:2222 accepted tcp:x.com:443 [vless-reality-vision -> direct] email: alice\n")
	f.Close()
	inc := w.collect()
	if len(inc) != 1 || inc[0].IP != "9.9.9.9" {
		t.Fatalf("incremental read = %+v", inc)
	}

	// Truncation (rotation) resets the offset.
	os.WriteFile(path, []byte("2026/07/12 10:00:00.000 from 2.2.2.2:1 accepted tcp:y:443 [vless-xtls-vision -> direct] email: bob\n"), 0o644)
	rot := w.collect()
	if len(rot) != 1 || rot[0].IP != "2.2.2.2" || rot[0].Email != "bob" {
		t.Fatalf("post-rotation read = %+v", rot)
	}
}

func TestAccessWatcherSkipsHistoryOnFirstRun(t *testing.T) {
	// An agent updated onto a node whose access log already holds weeks of
	// traffic must not report every IP in it as a client seen just now.
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	state := accessStatePath(filepath.Join(dir, "users.json"))
	history := ""
	for _, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		history += "2026/07/12 09:00:00.000 from " + ip + ":1 accepted tcp:a:443 [vless-reality-vision -> direct] email: alice\n"
	}
	if err := os.WriteFile(path, []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}
	w := newAccessWatcher(path, state)
	if got := w.collect(); len(got) != 0 {
		t.Fatalf("first run reported %d historical devices, want 0", len(got))
	}
	// Traffic after that point is still reported.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("2026/07/12 09:10:00.000 from 9.9.9.9:1 accepted tcp:a:443 [vless-reality-vision -> direct] email: alice\n")
	f.Close()
	if got := w.collect(); len(got) != 1 || got[0].IP != "9.9.9.9" {
		t.Fatalf("new line not reported: %+v", got)
	}
}

func TestAccessOffsetSurvivesAgentRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	state := accessStatePath(filepath.Join(dir, "users.json"))
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	w := newAccessWatcher(path, state)
	w.collect() // establish the read position
	line := "2026/07/12 09:00:00.123 from 1.2.3.4:5678 accepted tcp:example.com:443 [vless-reality-vision -> direct] email: alice\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := w.collect(); len(got) != 1 {
		t.Fatalf("read = %d devices, want 1", len(got))
	}

	// The agent is replaced (an update). Re-reading from 0 here is what made a
	// node report every IP it had ever seen as an active client.
	if got := newAccessWatcher(path, state).collect(); len(got) != 0 {
		t.Fatalf("restarted agent re-read the log: %+v", got)
	}
}

func TestAccessWatcherDetectsRotationByInode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	state := accessStatePath(filepath.Join(dir, "users.json"))
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	w := newAccessWatcher(path, state)
	w.collect() // establish the read position
	short := "2026/07/12 09:00:00.123 from 1.2.3.4:5678 accepted tcp:a:443 [vless-reality-vision -> direct] email: alice\n"
	if err := os.WriteFile(path, []byte(short), 0o644); err != nil {
		t.Fatal(err)
	}
	w.collect()

	// Rotated the way logrotate does it — a new file moved into place — and it is
	// already longer than the old offset, so the size check alone would miss it.
	rotated := short + "2026/07/12 09:00:01.000 from 5.6.7.8:1234 accepted tcp:b:443 [vless-xtls-vision -> direct] email: bob\n"
	fresh := path + ".new"
	if err := os.WriteFile(fresh, []byte(rotated), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(fresh, path); err != nil {
		t.Fatal(err)
	}
	if got := w.collect(); len(got) != 2 {
		t.Fatalf("post-rotation read = %d devices, want 2 (whole new file)", len(got))
	}
}

func TestLineTime(t *testing.T) {
	now := time.Now().Unix()
	want := time.Date(2026, 7, 12, 9, 0, 0, 0, time.Local).Unix()
	line := "2026/07/12 09:00:00.123 from 1.2.3.4:5678 accepted tcp:a:443 [vless-reality-vision -> direct] email: alice"
	if got := lineTime(line, now); got != want {
		t.Errorf("lineTime = %d, want %d", got, want)
	}
	if got := lineTime("garbage", now); got != now {
		t.Errorf("unparseable line = %d, want now (%d)", got, now)
	}
	// A clock/zone mismatch must not park a device in the future, where it would
	// count as active forever.
	future := time.Now().Add(48*time.Hour).Format("2006/01/02 15:04:05") + " from 1.2.3.4:1 accepted tcp:a:443 [x -> direct] email: a"
	if got := lineTime(future, now); got != now {
		t.Errorf("future line = %d, want clamped to now (%d)", got, now)
	}
}
