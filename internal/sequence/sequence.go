// Package sequence implements a tiny state-based replicated text sequence.
// It is the next-step CRDT foundation for character-level collaboration: each
// element has a stable id, an insertion parent, and a tombstone flag. Merge is
// a union of elements; rendering walks the resulting tree deterministically.
package sequence

import (
	"encoding/json"
	"sort"
	"strings"
)

// Node is one replicated text element.
type Node struct {
	ID      string `json:"id"`
	After   string `json:"after,omitempty"`
	Text    string `json:"text,omitempty"`
	Deleted bool   `json:"d,omitempty"`
}

// Doc is a state-based sequence CRDT.
type Doc struct {
	nodes map[string]Node
}

// New returns an empty sequence document.
func New() *Doc { return &Doc{nodes: map[string]Node{}} }

// Apply merges a single node into the document.
func (d *Doc) Apply(n Node) {
	if n.ID == "" {
		return
	}
	if cur, ok := d.nodes[n.ID]; ok {
		if nodeBetter(n, cur) {
			d.nodes[n.ID] = n
		}
		return
	}
	d.nodes[n.ID] = n
}

// Insert records a live node at the given parent id.
func (d *Doc) Insert(id, after, text string) { d.Apply(Node{ID: id, After: after, Text: text}) }

// Delete tombstones a node id.
func (d *Doc) Delete(id string) { d.Apply(Node{ID: id, Deleted: true}) }

// Merge folds the full state of another replica into this one.
func (d *Doc) Merge(other *Doc) {
	for _, n := range other.nodes {
		d.Apply(n)
	}
}

// Snapshot returns a copy of the CRDT state.
func (d *Doc) Snapshot() map[string]Node {
	out := make(map[string]Node, len(d.nodes))
	for id, n := range d.nodes {
		out[id] = n
	}
	return out
}

// Text renders the visible sequence as a single string.
func (d *Doc) Text() string {
	var b strings.Builder
	visited := map[string]bool{}
	var walk func(parent string)
	walk = func(parent string) {
		children := d.children(parent)
		for _, id := range children {
			if visited[id] {
				continue
			}
			visited[id] = true
			n := d.nodes[id]
			if !n.Deleted {
				b.WriteString(n.Text)
			}
			walk(id)
		}
	}
	walk("")
	return b.String()
}

// VisibleIDs returns ids in render order, excluding tombstones.
func (d *Doc) VisibleIDs() []string {
	out := []string{}
	visited := map[string]bool{}
	var walk func(parent string)
	walk = func(parent string) {
		children := d.children(parent)
		for _, id := range children {
			if visited[id] {
				continue
			}
			visited[id] = true
			n := d.nodes[id]
			if !n.Deleted {
				out = append(out, id)
			}
			walk(id)
		}
	}
	walk("")
	return out
}

func (d *Doc) children(parent string) []string {
	ids := make([]string, 0)
	for id, n := range d.nodes {
		if n.After == parent {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func nodeBetter(a, b Node) bool {
	if a.After != b.After {
		return a.After > b.After
	}
	if a.Deleted != b.Deleted {
		return a.Deleted
	}
	if a.Text != b.Text {
		return a.Text > b.Text
	}
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) > string(bb)
}
