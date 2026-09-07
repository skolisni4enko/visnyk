package ui

import (
	"testing"
	"time"
)

// GetConnectionsStatus must never block on network: empty cache serves
// cheap local flags, warm cache is returned as-is.
func TestGetConnectionsStatusInstant(t *testing.T) {
	a := &App{}
	start := time.Now()
	st := a.GetConnectionsStatus()
	if time.Since(start) > 2*time.Second {
		t.Fatal("status must be instant without services")
	}
	if st.WhatsApp.OK || st.Telegram.OK || st.Viber.OK {
		t.Fatalf("all channels must be down on empty app, got %+v", st)
	}
}

// Warm cache is served without triggering a refresh.
func TestGetConnectionsStatusCached(t *testing.T) {
	a := &App{}
	want := ConnectionsStatus{WhatsApp: ChannelStatus{Connected: true, LoggedIn: true, OK: true}}
	a.healthMu.Lock()
	a.healthCache = want
	a.healthAt = time.Now()
	a.healthMu.Unlock()

	st := a.GetConnectionsStatus()
	if !st.WhatsApp.OK {
		t.Fatalf("want cached status, got %+v", st)
	}
	a.healthMu.Lock()
	inFlight := a.healthInFlight
	a.healthMu.Unlock()
	if inFlight {
		t.Fatal("warm cache must not trigger background refresh")
	}
}

// refreshHealth with nil services completes instantly and caches.
func TestRefreshHealthNilServices(t *testing.T) {
	a := &App{}
	a.refreshHealth()
	a.healthMu.Lock()
	at := a.healthAt
	cached := a.healthCache
	a.healthMu.Unlock()
	if at.IsZero() {
		t.Fatal("refresh must stamp the cache")
	}
	if cached.WhatsApp.OK || cached.Telegram.OK {
		t.Fatalf("nil services mean disconnected, got %+v", cached)
	}
	if cached.Viber.Error == "" {
		t.Fatal("viber stub must carry an error note")
	}
}

// Transitions must not panic without store/logger and must be idempotent.
func TestLogHealthTransition(t *testing.T) {
	a := &App{}
	a.logHealthTransition("telegram", true, true, "")
	a.logHealthTransition("telegram", true, false, "boom")
	a.logHealthTransition("telegram", false, true, "")
}

// Monitor start is idempotent, stop is idempotent.
func TestHealthMonitorLifecycle(t *testing.T) {
	a := &App{}
	a.startHealthMonitor()
	a.startHealthMonitor()
	// initial refresh fires after 3s; stop before that to stay hermetic.
	a.stopHealthMonitor()
	a.stopHealthMonitor()
}
