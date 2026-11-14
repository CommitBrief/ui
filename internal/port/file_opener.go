// SPDX-License-Identifier: GPL-3.0-or-later

package port

import (
	"context"
	"errors"
)

// ErrFileMissing is returned by a FileOpener when the file does not exist (or
// is not a regular file).
var ErrFileMissing = errors.New("file opener: no such file")

// FileOpener opens a local file with the operating system's default
// application. The caller decides which file; the webview never supplies a
// path.
type FileOpener interface {
	// Open returns ErrFileMissing when path is not an existing regular file,
	// and any other error when the file could not be handed to the system.
	Open(ctx context.Context, path string) error
}
