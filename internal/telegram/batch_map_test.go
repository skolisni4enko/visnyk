package telegram

import (
	"fmt"
	"testing"

	"github.com/gotd/td/tg"
)

// These tests pin the phone mapping produced by BatchImport. They exist
// because the previous implementation keyed the found map by the
// normalizePhone form (no "+") while cascade looked phones up as E.164
// (+380...), so every contact silently looked "not on Telegram" and the whole
// channel was skipped in a broadcast.

func testImportResponse(users []*tg.User, imported []tg.ImportedContact) *tg.ContactsImportedContacts {
	res := &tg.ContactsImportedContacts{Imported: imported}
	for _, u := range users {
		res.Users = append(res.Users, u)
	}
	return res
}

func TestMapImportedChunkClientIDMapping(t *testing.T) {
	// chunk holds normalizePhone output: digits without "+".
	chunk := []string{"380991234500", "380991234501", "380991234502"}
	u0 := &tg.User{ID: 11, AccessHash: 111}
	u2 := &tg.User{ID: 22, AccessHash: 222, Phone: "380991234502"}
	res := testImportResponse([]*tg.User{u0, u2}, []tg.ImportedContact{
		{UserID: 11, ClientID: 0},
		{UserID: 22, ClientID: 2},
	})

	found, users := mapImportedChunk(chunk, res)

	// Keys must be canonical E.164, the form cascade looks up.
	wantFound := map[string]bool{"+380991234500": true, "+380991234502": true}
	if len(found) != len(wantFound) {
		t.Fatalf("want %d found phones, got %d: %v", len(wantFound), len(found), found)
	}
	for ph := range wantFound {
		if !found[ph] {
			t.Errorf("want phone %s marked found, got %v", ph, found)
		}
	}
	// ClientID 1 was not imported: that phone must stay out.
	if found["+380991234501"] {
		t.Errorf("phone without an Imported entry must not be found: %v", found)
	}
	// ClientID indexes into the chunk, so each phone maps to its own user.
	if users["+380991234500"] != u0 {
		t.Errorf("want user %d for +380991234500, got %+v", u0.ID, users["+380991234500"])
	}
	if users["+380991234502"] != u2 {
		t.Errorf("want user %d for +380991234502, got %+v", u2.ID, users["+380991234502"])
	}
}

// A client_id of zero must not be treated as "assign to chunk[0]" for every
// imported user: only the entry whose ClientID really is the first index maps
// to the first phone.
func TestMapImportedChunkZeroClientIDIsNotSpread(t *testing.T) {
	chunk := []string{"380991234500", "380991234501"}
	u0 := &tg.User{ID: 11, AccessHash: 111}
	u1 := &tg.User{ID: 22, AccessHash: 222}
	res := testImportResponse([]*tg.User{u0, u1}, []tg.ImportedContact{
		{UserID: 11, ClientID: 0},
		{UserID: 22, ClientID: 0},
	})

	found, users := mapImportedChunk(chunk, res)

	// Both entries point at the first index; the second phone stays unknown
	// rather than being silently attached to the wrong user.
	if !found["+380991234500"] {
		t.Errorf("want +380991234500 found, got %v", found)
	}
	if found["+380991234501"] {
		t.Errorf("+380991234501 must not be found from a duplicate zero client_id: %v", found)
	}
	if users["+380991234500"] == nil {
		t.Errorf("want a user for +380991234500")
	}
}

func TestMapImportedChunkKnownContactByPhone(t *testing.T) {
	chunk := []string{"380991234500", "380991234501"}
	known := &tg.User{ID: 33, AccessHash: 333, Phone: "380991234501"}
	foreign := &tg.User{ID: 44, AccessHash: 444, Phone: "380991234999"}
	// Contacts already known to the account are not listed in Imported.
	res := testImportResponse([]*tg.User{known, foreign}, nil)

	found, users := mapImportedChunk(chunk, res)

	if !found["+380991234501"] {
		t.Errorf("want known contact found by its phone, got %v", found)
	}
	if found["+380991234999"] {
		t.Errorf("a phone outside the chunk must not be reported found: %v", found)
	}
	if users["+380991234501"] != known {
		t.Errorf("want the known user mapped to +380991234501")
	}
}

func TestMapImportedChunkIgnoresOutOfRangeClientID(t *testing.T) {
	chunk := []string{"380991234500"}
	res := testImportResponse([]*tg.User{{ID: 11, AccessHash: 111}}, []tg.ImportedContact{
		{UserID: 11, ClientID: 7},
	})

	found, _ := mapImportedChunk(chunk, res)

	if len(found) != 0 {
		t.Errorf("out-of-range client_id must not map anything, got %v", found)
	}
}

func TestBuildChunkContactsClientIDs(t *testing.T) {
	for _, size := range []int{1, 3, 20, 21} {
		chunk := make([]string, 0, size)
		for i := 0; i < size; i++ {
			chunk = append(chunk, fmt.Sprintf("3809912%04d", i))
		}
		contacts := buildChunkContacts(chunk)
		if len(contacts) != size {
			t.Fatalf("size %d: want %d contacts, got %d", size, size, len(contacts))
		}
		for i, c := range contacts {
			if c.ClientID != int64(i) {
				t.Errorf("size %d: contact %d has ClientID %d, want %d", size, i, c.ClientID, i)
			}
			if c.Phone != chunk[i] {
				t.Errorf("size %d: contact %d phone %q, want %q", size, i, c.Phone, chunk[i])
			}
			if len(c.FirstName) > 20 {
				t.Errorf("size %d: contact %d name %q longer than 20 chars", size, i, c.FirstName)
			}
		}
	}
}

// TestBuildChunkContactsIDsRestartPerChunk pins that ClientID is a per-chunk
// index, because BatchImport calls buildChunkContacts once per chunk.
func TestBuildChunkContactsIDsRestartPerChunk(t *testing.T) {
	first := buildChunkContacts([]string{"380991234500", "380991234501"})
	second := buildChunkContacts([]string{"380991234502", "380991234503"})
	if first[0].ClientID != 0 || second[0].ClientID != 0 {
		t.Errorf("ClientID must restart at 0 per chunk: first=%d second=%d", first[0].ClientID, second[0].ClientID)
	}
}

func TestMapImportedChunkNilResponse(t *testing.T) {
	found, users := mapImportedChunk([]string{"380991234500"}, nil)
	if len(found) != 0 || len(users) != 0 {
		t.Errorf("nil response must map nothing, got found=%v users=%v", found, users)
	}
}

func TestMapImportedChunkIgnoresUnknownUserID(t *testing.T) {
	chunk := []string{"380991234500"}
	res := testImportResponse(nil, []tg.ImportedContact{{UserID: 99, ClientID: 0}})
	found, users := mapImportedChunk(chunk, res)
	if len(found) != 0 || len(users) != 0 {
		t.Errorf("imported entry without a matching user must map nothing, got found=%v users=%v", found, users)
	}
}

func TestBatchKeyMatchesCascadeLookup(t *testing.T) {
	// cascade looks up found[common.CleanPhoneE164(c.NormalizedPhone)], so the
	// key must be E.164 with the leading "+" kept, and must be idempotent.
	for _, tc := range []struct{ in, want string }{
		{"+380991234567", "+380991234567"},
		{"380991234567", "+380991234567"},
		{"099 123-45-67", "+0991234567"}, // batchKey is not a country normalizer
		{"", ""},
	} {
		if got := batchKey(tc.in); got != tc.want {
			t.Errorf("batchKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if got := batchKey(tc.want); got != tc.want {
			t.Errorf("batchKey not idempotent: batchKey(%q) = %q", tc.want, got)
		}
	}
}
