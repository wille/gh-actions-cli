package command

import "errors"

// ErrCheckFailed signals that a command found problems and the process should
// exit non-zero, without printing an additional error line — the human-readable
// findings have already been written to stdout. Used by `pin` (unpinned actions)
// so it can gate CI.
var ErrCheckFailed = errors.New("")
