package panel

import (
	"testing"
	"time"

	"xuanwu/internal/wire"
)

func TestMonthlyResetDue(t *testing.T) {
	utc := time.UTC
	mk := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 12, 0, 0, 0, utc) }

	cases := []struct {
		name      string
		now       time.Time
		resetDay  int64
		lastReset int64
		wantDue   bool
	}{
		{"disabled", mk(2026, 3, 15), 0, 0, false},
		{"before reset day", mk(2026, 3, 10), 15, 0, false},
		{"on reset day, never reset", mk(2026, 3, 15), 15, 0, true},
		{"after reset day, never reset", mk(2026, 3, 20), 15, 0, true},
		{"already reset this month", mk(2026, 3, 20), 15, mk(2026, 3, 15).Unix(), false},
		{"reset was last month", mk(2026, 3, 20), 15, mk(2026, 2, 15).Unix(), true},
		{"day clamped in Feb", mk(2026, 2, 28), 28, 0, true},
	}
	for _, c := range cases {
		_, due := monthlyResetDue(c.now, c.resetDay, c.lastReset)
		if due != c.wantDue {
			t.Errorf("%s: due=%v want %v", c.name, due, c.wantDue)
		}
	}
}

func TestRecordRateSplitsDirections(t *testing.T) {
	a := &App{}
	a.recordRate(1, 0, 0) // first report only establishes the interval baseline
	if got := a.getNodeRate(1); got.Up != 0 || got.Down != 0 {
		t.Fatalf("first report produced a rate: %+v", got)
	}
	// Backdate the baseline instead of sleeping: rates are derived per second.
	a.rateMu.Lock()
	a.rateLast[1] = time.Now().Unix() - 10
	a.rateMu.Unlock()

	a.recordRate(1, 1000, 4000)
	got := a.getNodeRate(1)
	if got.Up != 100 {
		t.Errorf("up = %v B/s, want 100", got.Up)
	}
	if got.Down != 400 {
		t.Errorf("down = %v B/s, want 400", got.Down)
	}
	if got.Total() != 500 {
		t.Errorf("total = %v B/s, want 500", got.Total())
	}

	// A stale node reports nothing rather than a frozen reading.
	a.rateMu.Lock()
	a.rateLast[1] = time.Now().Unix() - rateStaleAfter - 1
	a.rateMu.Unlock()
	if got := a.getNodeRate(1); got != (throughput{}) {
		t.Errorf("stale rate = %+v, want zero", got)
	}
}

func TestSyncNodeRecordsOnlyDeliveredPushes(t *testing.T) {
	s := newTestStore(t)
	app := &App{store: s, activeCache: map[int64]string{}}
	app.hub = NewHub(app)

	nid, _ := s.CreateNode(&Node{Name: "n", Token: "nt"})
	uid, _ := s.CreateUser(&User{Username: "a", UUID: "u1", SubToken: "s1", Enabled: true})
	if err := s.SetUserNodes(uid, []int64{nid}); err != nil {
		t.Fatal(err)
	}
	cached := func() (string, bool) {
		app.activeMu.Lock()
		defer app.activeMu.Unlock()
		sig, ok := app.activeCache[nid]
		return sig, ok
	}

	// A write queue that cannot accept anything: the push is dropped on the floor.
	blocked := &nodeConn{nodeID: nid, send: make(chan wire.Msg)}
	app.hub.conns[nid] = blocked
	app.syncNode(nid)
	if _, ok := cached(); ok {
		t.Fatal("a dropped push was recorded as delivered")
	}

	// A queue with room: the config lands and only now is the signature recorded.
	live := &nodeConn{nodeID: nid, send: make(chan wire.Msg, 2)}
	app.hub.conns[nid] = live
	app.syncNode(nid)
	if len(live.send) != 1 {
		t.Fatalf("queued %d config messages, want 1", len(live.send))
	}
	if _, ok := cached(); !ok {
		t.Fatal("delivered push was not recorded")
	}

	// An unchanged user set must not re-push.
	<-live.send
	app.syncNode(nid)
	if len(live.send) != 0 {
		t.Fatal("re-pushed an unchanged user set")
	}

	// forceSyncNode that cannot reach the node must drop the cached signature,
	// otherwise syncNode would never retry: the user set alone is unchanged.
	app.hub.conns[nid] = blocked
	app.forceSyncNode(nid)
	if _, ok := cached(); ok {
		t.Fatal("undelivered force-sync left a signature behind")
	}
}
