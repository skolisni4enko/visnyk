package common

import (
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"
)

func TestFloodWaitDuration_Typed(t *testing.T) {
	err := tgerr.New(420, "FLOOD_WAIT_110")
	d, ok := FloodWaitDuration(err)
	if !ok {
		t.Fatal("want flood wait detected")
	}
	if d != 110*time.Second {
		t.Fatalf("want 110s, got %v", d)
	}
}

func TestFloodWaitDuration_Wrapped(t *testing.T) {
	err := errors.New("ImportContacts: rpc error code 420: FLOOD_WAIT (96)")
	d, ok := FloodWaitDuration(err)
	if !ok {
		t.Fatal("want flood wait detected in wrapped text")
	}
	if d != 96*time.Second {
		t.Fatalf("want 96s, got %v", d)
	}
}

func TestFloodWaitDuration_Negative(t *testing.T) {
	if _, ok := FloodWaitDuration(nil); ok {
		t.Fatal("nil must not be flood")
	}
	if _, ok := FloodWaitDuration(errors.New("boom")); ok {
		t.Fatal("random error must not be flood")
	}
}

func TestIsTelegramAuthError(t *testing.T) {
	if !IsTelegramAuthError(tgerr.New(401, "AUTH_KEY_UNREGISTERED")) {
		t.Fatal("401 must be auth error")
	}
	if !IsTelegramAuthError(errors.New("ImportContacts: rpc error code 401: AUTH_KEY_UNREGISTERED")) {
		t.Fatal("wrapped 401 text must be auth error")
	}
	if IsTelegramAuthError(tgerr.New(420, "FLOOD_WAIT_10")) {
		t.Fatal("flood must not be auth error")
	}
}

func TestIsTelegramSkippable(t *testing.T) {
	for _, s := range []string{
		"send: rpc error code 403: PRIVACY_PREMIUM_REQUIRED",
		"USER_IS_BLOCKED",
		"PEER_FLOOD",
	} {
		if !IsTelegramSkippable(errors.New(s)) {
			t.Fatalf("%q must be skippable", s)
		}
	}
	if IsTelegramSkippable(errors.New("FLOOD_WAIT_10")) {
		t.Fatal("flood must not be skippable")
	}
}

func TestIsTelegramNotFound(t *testing.T) {
	if !IsTelegramNotFound(tgerr.New(400, "PHONE_NOT_OCCUPIED")) {
		t.Fatal("PHONE_NOT_OCCUPIED must be not-found")
	}
	if IsTelegramNotFound(errors.New("FLOOD_WAIT_10")) {
		t.Fatal("flood must not be not-found")
	}
}
