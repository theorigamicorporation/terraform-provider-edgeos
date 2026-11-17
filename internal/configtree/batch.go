// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

package configtree

import (
	"encoding/json"
	"strings"
)

// Batch is a set of changes applied and committed in one go. On the device
// all DELETE operations run before the SET operations.
type Batch struct {
	set     map[string]any
	del     map[string]any
	setList []Op
	delList []Path
}

// Op is a single SET operation.
type Op struct {
	Path  Path
	Value Value
}

// NewBatch returns an empty batch.
func NewBatch() *Batch {
	return &Batch{set: map[string]any{}, del: map[string]any{}}
}

// Set adds a SET operation for path.
func (b *Batch) Set(path Path, v Value) {
	insertSet(b.set, path, v.JSON())
	b.setList = append(b.setList, Op{Path: NewPath(path...), Value: v})
}

// Delete adds a DELETE operation for path and everything below it.
func (b *Batch) Delete(path Path) {
	insertDelete(b.del, path)
	b.delList = append(b.delList, NewPath(path...))
}

// Merge appends all operations of o to b.
func (b *Batch) Merge(o *Batch) {
	for _, p := range o.delList {
		b.Delete(p)
	}
	for _, op := range o.setList {
		b.Set(op.Path, op.Value)
	}
}

// Empty reports whether the batch has no operations.
func (b *Batch) Empty() bool {
	return len(b.setList) == 0 && len(b.delList) == 0
}

// Sets returns the SET operations in the order they were added.
func (b *Batch) Sets() []Op { return append([]Op{}, b.setList...) }

// Deletes returns the DELETE paths in the order they were added.
func (b *Batch) Deletes() []Path { return append([]Path{}, b.delList...) }

// SetTree returns the nested SET document.
func (b *Batch) SetTree() map[string]any { return b.set }

// DeleteTree returns the nested DELETE document.
func (b *Batch) DeleteTree() map[string]any { return b.del }

// MarshalJSON renders the body of a batch.json request.
func (b *Batch) MarshalJSON() ([]byte, error) {
	body := map[string]any{}
	if len(b.del) > 0 {
		body["DELETE"] = b.del
	}
	if len(b.set) > 0 {
		body["SET"] = b.set
	}
	return json.Marshal(body)
}

// String renders the batch as CLI commands, one per line. It is used in logs
// and error messages.
func (b *Batch) String() string {
	var lines []string
	for _, p := range b.delList {
		lines = append(lines, "delete "+p.String())
	}
	for _, op := range b.setList {
		switch op.Value.Kind {
		case KindFlag:
			lines = append(lines, "set "+op.Path.String())
		default:
			for _, v := range op.Value.Values {
				lines = append(lines, "set "+op.Path.Child(v).String())
			}
		}
	}
	return strings.Join(lines, "\n")
}
