package crdt

// Timestamp is a Lamport logical timestamp with a node identifier used for
// deterministic tie-breaking. Ordering rule: the higher Counter wins; ties
// are broken by comparing Node strings. This yields a *total order* over all
// writes in the cluster without requiring synchronized wall clocks.
//
// Trade-off (see docs/architecture/DESIGN.md): because ordering is causal, not wall-clock based,
// "last writer wins" means "last in causal order," which can differ from real
// time when two replicas edit while partitioned. This is the deliberate,
// documented cost of avoiding clock-skew bugs.
type Timestamp struct {
	Counter uint64 `json:"c"`
	Node    string `json:"n"`
}

// After reports whether a should win over b under the LWW ordering.
func (a Timestamp) After(b Timestamp) bool {
	if a.Counter != b.Counter {
		return a.Counter > b.Counter
	}
	return a.Node > b.Node
}

// Clock is a per-replica Lamport clock. It is not safe for concurrent use;
// callers serialize access (the sync hub owns one clock per room).
type Clock struct {
	node    string
	counter uint64
}

// NewClock returns a clock stamped with this replica's node id.
func NewClock(node string) *Clock { return &Clock{node: node} }

// Tick advances the clock for a local event and returns the new timestamp.
func (c *Clock) Tick() Timestamp {
	c.counter++
	return Timestamp{Counter: c.counter, Node: c.node}
}

// Observe merges a remote timestamp so this clock never lags behind a
// causally-prior event (the Lamport receive rule).
func (c *Clock) Observe(t Timestamp) {
	if t.Counter > c.counter {
		c.counter = t.Counter
	}
}

// Node returns this clock's replica id.
func (c *Clock) Node() string { return c.node }
