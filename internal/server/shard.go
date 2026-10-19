package server

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
)

// ShardPlanner picks a deterministic owner node for a room. It is a local
// rendezvous-hashing primitive that makes future room sharding and pub/sub
// fan-out easier to layer on top of the existing single-process hub.
type ShardPlanner struct {
	nodes []string
}

// NewShardPlanner returns a planner with a stable, deduplicated node list.
func NewShardPlanner(nodes ...string) *ShardPlanner {
	seen := map[string]struct{}{}
	filtered := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node == "" {
			continue
		}
		if _, ok := seen[node]; ok {
			continue
		}
		seen[node] = struct{}{}
		filtered = append(filtered, node)
	}
	sort.Strings(filtered)
	return &ShardPlanner{nodes: filtered}
}

// Nodes returns a copy of the known node IDs.
func (p *ShardPlanner) Nodes() []string {
	if p == nil || len(p.nodes) == 0 {
		return nil
	}
	out := make([]string, len(p.nodes))
	copy(out, p.nodes)
	return out
}

// Owner returns the deterministic owner node for a room. When no cluster nodes
// are configured, the empty string means "single-node/local only".
func (p *ShardPlanner) Owner(roomID string) string {
	if p == nil || len(p.nodes) == 0 {
		return ""
	}
	var (
		bestNode  string
		bestScore uint64
	)
	for i, node := range p.nodes {
		score := rendezvousScore(roomID, node)
		if i == 0 || score > bestScore {
			bestNode, bestScore = node, score
		}
	}
	return bestNode
}

// Owns reports whether nodeID is the chosen owner for roomID.
func (p *ShardPlanner) Owns(roomID, nodeID string) bool {
	owner := p.Owner(roomID)
	return owner == "" || owner == nodeID
}

func rendezvousScore(roomID, nodeID string) uint64 {
	sum := sha256.Sum256([]byte(roomID + "\x00" + nodeID))
	return binary.BigEndian.Uint64(sum[:8])
}
