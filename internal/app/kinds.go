// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"fmt"

	"github.com/CommitBrief/ui/internal/domain/job"
	"github.com/CommitBrief/ui/internal/port"
)

// KindRegistry maps a job kind to its implementation. The composition root
// builds it once; the run service looks kinds up by name.
type KindRegistry struct {
	kinds map[job.Kind]port.JobKind
}

// NewKindRegistry registers the kinds. Two implementations of one kind, or a
// kind whose name is not a valid kind name, is a programming error and
// returned as one.
func NewKindRegistry(kinds ...port.JobKind) (*KindRegistry, error) {
	reg := &KindRegistry{kinds: make(map[job.Kind]port.JobKind, len(kinds))}
	for _, k := range kinds {
		name := k.Kind()
		if !name.Valid() {
			return nil, fmt.Errorf("app: %q is not a valid job kind name", name)
		}
		if _, dup := reg.kinds[name]; dup {
			return nil, fmt.Errorf("app: job kind %q is registered twice", name)
		}
		reg.kinds[name] = k
	}
	return reg, nil
}

// Get returns the implementation of a kind.
func (r *KindRegistry) Get(k job.Kind) (port.JobKind, bool) {
	impl, ok := r.kinds[k]
	return impl, ok
}
