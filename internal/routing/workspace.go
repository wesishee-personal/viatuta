package routing

import (
	"math"
	"sync"
)

// workspace holds the scratch arrays one search needs.
//
// These are large — on Austin's graph with a stress budget, the state arrays
// come to roughly 28 MB — and allocating them per request put real pressure
// on the garbage collector. Pooling makes steady-state allocation nearly zero
// at the cost of holding one workspace per concurrent search.
type workspace struct {
	dist  []float64
	usedM []float32
	prev  []int32
	done  []bool

	// hcache memoises the heuristic per NODE rather than per state. Many
	// states share a destination node, and a haversine is far more expensive
	// than an array read.
	hcache []float32

	queue stateQueue
}

var wsPool sync.Pool

// getWorkspace returns a workspace sized for this search, reusing a pooled
// one when it is large enough.
func getWorkspace(numStates, numNodes int) *workspace {
	w, _ := wsPool.Get().(*workspace)
	if w == nil {
		w = &workspace{}
	}

	w.dist = growFloat64(w.dist, numStates)
	w.usedM = growFloat32(w.usedM, numStates)
	w.prev = growInt32(w.prev, numStates)
	w.done = growBool(w.done, numStates)
	w.hcache = growFloat32(w.hcache, numNodes)

	// Reset only the prefix this search will touch. A grown slice may be
	// longer than needed; the extra tail is never read.
	d := w.dist[:numStates]
	for i := range d {
		d[i] = math.Inf(1)
	}
	p := w.prev[:numStates]
	for i := range p {
		p[i] = -1
	}
	clear(w.done[:numStates])
	h := w.hcache[:numNodes]
	for i := range h {
		h[i] = -1 // sentinel: not yet computed
	}

	w.queue = w.queue[:0]
	return w
}

func (w *workspace) release() {
	// Drop the reference to any leftover queue contents so they can be
	// collected, then return the buffers for reuse.
	w.queue = w.queue[:0]
	wsPool.Put(w)
}

// The grow helpers reuse capacity when possible and otherwise allocate with
// some headroom, so a slightly larger graph does not force a reallocation.

func growFloat64(s []float64, n int) []float64 {
	if cap(s) >= n {
		return s[:n]
	}
	return make([]float64, n)
}

func growFloat32(s []float32, n int) []float32 {
	if cap(s) >= n {
		return s[:n]
	}
	return make([]float32, n)
}

func growInt32(s []int32, n int) []int32 {
	if cap(s) >= n {
		return s[:n]
	}
	return make([]int32, n)
}

func growBool(s []bool, n int) []bool {
	if cap(s) >= n {
		return s[:n]
	}
	return make([]bool, n)
}
