package server

import (
	"fmt"
	"io"
	"strconv"
	"sync"
)

// histogram is a small fixed-bucket Prometheus histogram. It avoids pulling in
// a client library while still exporting cumulative buckets, _sum, and _count.
type histogram struct {
	mu      sync.Mutex
	buckets []float64
	counts  []uint64
	sum     float64
	count   uint64
}

func newHistogram(buckets []float64) *histogram {
	return &histogram{buckets: buckets, counts: make([]uint64, len(buckets))}
}

func (h *histogram) observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sum += v
	h.count++
	for i, upper := range h.buckets {
		if v <= upper {
			h.counts[i]++
		}
	}
}

func (h *histogram) write(w io.Writer, name, help string) {
	h.mu.Lock()
	buckets := append([]float64(nil), h.buckets...)
	counts := append([]uint64(nil), h.counts...)
	sum, count := h.sum, h.count
	h.mu.Unlock()

	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s histogram\n", name)
	for i, upper := range buckets {
		fmt.Fprintf(w, "%s_bucket{le=%q} %d\n", name, strconv.FormatFloat(upper, 'g', -1, 64), counts[i])
	}
	fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n", name, count)
	fmt.Fprintf(w, "%s_sum %s\n", name, strconv.FormatFloat(sum, 'g', -1, 64))
	fmt.Fprintf(w, "%s_count %d\n", name, count)
}
