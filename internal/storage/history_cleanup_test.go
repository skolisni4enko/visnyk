package storage

import (
	"strings"
	"testing"
	"time"
)

func TestAddBatchErrorUpdatesOnlyMatchingHistory(t *testing.T) {
	store, err := OpenWithKey(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, entry := range []HistoryEntry{
		{Phone: "first", Normalized: "+380991234500", Channel: "telegram", Status: "sent", SentAt: time.Now(), BatchID: "batch-a"},
		{Phone: "second", Normalized: "+380991234501", Channel: "telegram", Status: "sent", SentAt: time.Now(), BatchID: "batch-b"},
	} {
		if err := store.AddHistory(entry); err != nil {
			t.Fatal(err)
		}
	}

	const note = "Telegram: temporary contacts cleanup failed: rpc timeout"
	updated, err := store.AddBatchError("batch-a", note)
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 {
		t.Errorf("want one history row updated, got %d", updated)
	}
	entries, err := store.ListHistoryPaged(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want two entries, got %d", len(entries))
	}
	for _, entry := range entries {
		switch entry.BatchID {
		case "batch-a":
			if !strings.Contains(entry.Error, note) {
				t.Errorf("matching batch error not saved: %+v", entry)
			}
		case "batch-b":
			if entry.Error != "" {
				t.Errorf("unrelated batch history changed: %+v", entry)
			}
		default:
			t.Errorf("unexpected batch ID: %+v", entry)
		}
	}
}

func TestAddBatchErrorReportsMissingHistoryRows(t *testing.T) {
	store, err := OpenWithKey(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	updated, err := store.AddBatchError("missing-batch", "Telegram cleanup failed")
	if err != nil {
		t.Fatal(err)
	}
	if updated != 0 {
		t.Errorf("want zero rows updated for absent batch, got %d", updated)
	}
}
