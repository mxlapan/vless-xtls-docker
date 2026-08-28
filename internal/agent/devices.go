package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"xuanwu/internal/wire"
)

// The access-log watcher extracts per-user client devices (distinct source IPs)
// from Xray's access log. It deliberately records only device-identifying
// metadata — source IP and which inbound was used — never the browsing
// destination, which stays private to the user.

// Example access-log line:
//
//	2026/07/12 09:00:00.123 from 1.2.3.4:5678 accepted tcp:example.com:443 [vless-reality-vision -> direct] email: alice
var accessRe = regexp.MustCompile(`from (\[[0-9a-fA-F:]+\]|[0-9.]+):\d+ accepted [^ ]+ \[([^\] ]+)[^\]]*\] email: (.+?)\s*$`)

type accessWatcher struct {
	mu     sync.Mutex
	path   string
	state  string // durable offset file; "" disables persistence
	offset int64
	inode  uint64
	seeded bool // whether a read position is established
}

// accessOffset is the durable read position. Keeping it only in memory meant
// every agent restart re-read the whole access log and re-reported every source
// IP in it, so a node showed hundreds of "active clients" right after an update.
type accessOffset struct {
	Offset int64  `json:"offset"`
	Inode  uint64 `json:"inode"`
}

func accessStatePath(usersFile string) string {
	return filepath.Join(filepath.Dir(usersFile), "access-offset.json")
}

func newAccessWatcher(path, state string) *accessWatcher {
	w := &accessWatcher{path: path, state: state}
	if state == "" {
		return w
	}
	if b, err := os.ReadFile(state); err == nil {
		var o accessOffset
		if json.Unmarshal(b, &o) == nil {
			w.offset, w.inode, w.seeded = o.Offset, o.Inode, true
		}
	}
	return w
}

// saveOffset persists the read position. The caller must hold w.mu.
func (w *accessWatcher) saveOffset() {
	if w.state == "" {
		return
	}
	b, err := json.Marshal(accessOffset{Offset: w.offset, Inode: w.inode})
	if err != nil {
		return
	}
	tmp := w.state + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, w.state)
}

// fileInode identifies the log file so a rotation is detected even when the new
// file has already grown past the old read position.
func fileInode(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}

// accessTimeLayout is the timestamp Xray prefixes each access-log line with,
// e.g. "2026/07/12 09:00:00.123".
const accessTimeLayout = "2006/01/02 15:04:05"

// lineTime reads a line's own timestamp. Stamping lines with the collection time
// instead would date a backlog — anything logged while the agent was down — as
// if it had just happened, inflating the "active clients" count. Xray writes
// local time, the same zone the agent runs in; anything unparseable or ahead of
// our clock (a zone mismatch) falls back to now.
func lineTime(line string, now int64) int64 {
	if len(line) < len(accessTimeLayout) {
		return now
	}
	t, err := time.ParseInLocation(accessTimeLayout, line[:len(accessTimeLayout)], time.Local)
	if err != nil {
		return now
	}
	if ts := t.Unix(); ts <= now {
		return ts
	}
	return now
}

// inboundKind maps an Xray inbound tag to a short device characteristic.
func inboundKind(tag string) string {
	switch {
	case strings.Contains(tag, "reality"):
		return "reality"
	case strings.Contains(tag, "tls"):
		return "tls"
	default:
		return tag
	}
}

// collect reads new access-log lines and returns aggregated per-user devices
// since the last call. Handles truncation/rotation by resetting the offset when
// the file shrinks.
func (w *accessWatcher) collect() []wire.DeviceItem {
	if w.path == "" {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	fi, err := os.Stat(w.path)
	if err != nil {
		return nil
	}
	ino := fileInode(fi)
	if !w.seeded {
		// No recorded position: this agent has never read this log. Start at the
		// end. The file can hold weeks of history that was reported long ago, and
		// reading it would report every IP in it as a client seen right now —
		// which is exactly what an agent updated onto a busy node used to do.
		w.offset, w.inode, w.seeded = fi.Size(), ino, true
		w.saveOffset()
		return nil
	}
	if ino != w.inode {
		w.offset, w.inode = 0, ino // a different file: rotated
	}
	if fi.Size() < w.offset {
		w.offset = 0 // truncated in place
	}
	if fi.Size() == w.offset {
		return nil
	}
	f, err := os.Open(w.path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if _, err := f.Seek(w.offset, 0); err != nil {
		return nil
	}

	type key struct{ email, ip string }
	agg := map[key]*wire.DeviceItem{}
	now := time.Now().Unix()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var read int64
	for sc.Scan() {
		line := sc.Text()
		read += int64(len(line)) + 1
		m := accessRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ip := strings.Trim(m[1], "[]")
		kind := inboundKind(m[2])
		email := strings.TrimSpace(m[3])
		if email == "" || email == "unknown" {
			continue
		}
		k := key{email, ip}
		d := agg[k]
		if d == nil {
			d = &wire.DeviceItem{Email: email, IP: ip, Inbound: kind}
			agg[k] = d
		}
		d.Conns++
		d.Inbound = kind
		if ts := lineTime(line, now); ts > d.LastSeen {
			d.LastSeen = ts
		}
	}
	w.offset += read
	w.saveOffset()

	if len(agg) == 0 {
		return nil
	}
	out := make([]wire.DeviceItem, 0, len(agg))
	for _, d := range agg {
		out = append(out, *d)
	}
	return out
}
