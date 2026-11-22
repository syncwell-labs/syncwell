package crdt

import (
	"encoding/json"
	"sync"
)

// PNCounter is a state-based Positive-Negative Counter CRDT (CvRDT).
// It maintains two independent G-Counters: P for positive increments
// and N for negative decrements per node replica.
//
// The value of the counter is sum(P) - sum(N).
// Merging two counters takes the component-wise maximum of both P and N,
// forming a bounded join-semilattice that guarantees commutativity,
// associativity, and idempotence.
type PNCounter struct {
	mu   sync.RWMutex
	node string
	p    map[string]uint64
	n    map[string]uint64
}

// PNCounterState represents the serializable state of a PN-Counter.
type PNCounterState struct {
	P map[string]uint64 `json:"p"`
	N map[string]uint64 `json:"n"`
}

// NewPNCounter creates an empty PN-Counter bound to a given node replica ID.
func NewPNCounter(node string) *PNCounter {
	return &PNCounter{
		node: node,
		p:    make(map[string]uint64),
		n:    make(map[string]uint64),
	}
}

// Node returns the replica ID owning this counter.
func (c *PNCounter) Node() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.node
}

// Inc increments the counter by 1 for this node.
func (c *PNCounter) Inc() int64 {
	return c.Add(1)
}

// Dec decrements the counter by 1 for this node.
func (c *PNCounter) Dec() int64 {
	return c.Sub(1)
}

// Add increments the counter by delta for this node.
func (c *PNCounter) Add(delta uint64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.p[c.node] += delta
	return c.computeValueLocked()
}

// Sub decrements the counter by delta for this node.
func (c *PNCounter) Sub(delta uint64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n[c.node] += delta
	return c.computeValueLocked()
}

// Value computes and returns the current scalar integer value.
func (c *PNCounter) Value() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.computeValueLocked()
}

func (c *PNCounter) computeValueLocked() int64 {
	var pSum, nSum int64
	for _, val := range c.p {
		pSum += int64(val)
	}
	for _, val := range c.n {
		nSum += int64(val)
	}
	return pSum - nSum
}

// State returns a copy of the underlying P and N maps.
func (c *PNCounter) State() PNCounterState {
	c.mu.RLock()
	defer c.mu.RUnlock()

	pCopy := make(map[string]uint64, len(c.p))
	for k, v := range c.p {
		pCopy[k] = v
	}
	nCopy := make(map[string]uint64, len(c.n))
	for k, v := range c.n {
		nCopy[k] = v
	}

	return PNCounterState{P: pCopy, N: nCopy}
}

// Merge joins another PNCounter state into this counter using component-wise max.
// Returns true if local state was modified.
func (c *PNCounter) Merge(other *PNCounter) bool {
	if other == nil {
		return false
	}
	otherState := other.State()
	return c.MergeState(otherState)
}

// MergeState joins a raw PNCounterState using component-wise max.
// Returns true if local state was modified.
func (c *PNCounter) MergeState(state PNCounterState) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	var modified bool
	for k, v := range state.P {
		if cur, ok := c.p[k]; !ok || v > cur {
			c.p[k] = v
			modified = true
		}
	}
	for k, v := range state.N {
		if cur, ok := c.n[k]; !ok || v > cur {
			c.n[k] = v
			modified = true
		}
	}
	return modified
}

// Equal returns true if two counters have identical P and N maps.
func (c *PNCounter) Equal(other *PNCounter) bool {
	if other == nil {
		return false
	}
	s1 := c.State()
	s2 := other.State()

	if len(s1.P) != len(s2.P) || len(s1.N) != len(s2.N) {
		return false
	}
	for k, v := range s1.P {
		if s2.P[k] != v {
			return false
		}
	}
	for k, v := range s1.N {
		if s2.N[k] != v {
			return false
		}
	}
	return true
}

// Clone creates an exact deep copy of this counter with the same node replica ID.
func (c *PNCounter) Clone() *PNCounter {
	c.mu.RLock()
	defer c.mu.RUnlock()

	clone := NewPNCounter(c.node)
	for k, v := range c.p {
		clone.p[k] = v
	}
	for k, v := range c.n {
		clone.n[k] = v
	}
	return clone
}

// MarshalJSON serializes the counter state to JSON.
func (c *PNCounter) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.State())
}

// UnmarshalJSON deserializes counter state from JSON.
func (c *PNCounter) UnmarshalJSON(data []byte) error {
	var state PNCounterState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.p == nil {
		c.p = make(map[string]uint64)
	}
	if c.n == nil {
		c.n = make(map[string]uint64)
	}
	for k, v := range state.P {
		c.p[k] = v
	}
	for k, v := range state.N {
		c.n[k] = v
	}
	return nil
}
