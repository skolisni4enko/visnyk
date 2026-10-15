// Package telegram — see AGENTS.md for responsibilities.
package telegram

import "visnyk/internal/cascade"

// Compile-time interface guarantees: the Service satisfies every optional
// cascade fast path. If a method is removed or its signature drifts, this
// file fails to build.
var (
	_ cascade.Messenger          = (*Service)(nil)
	_ cascade.DirectSender       = (*Service)(nil)
	_ cascade.MediaSender        = (*Service)(nil)
	_ cascade.BatchImporter      = (*Service)(nil)
	_ cascade.BatchSender        = (*Service)(nil)
	_ cascade.BatchResolver      = (*Service)(nil)
	_ cascade.BatchMediaResolver = (*Service)(nil)
)
