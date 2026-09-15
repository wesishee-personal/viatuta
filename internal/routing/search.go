// Package routing contains the graph search algorithms.
//
// It knows nothing about bicycles. Costs and constraints arrive as functions
// supplied by the caller, which is what keeps the safety model tunable
// without touching search code — see internal/safety.
package routing

import (
	"errors"

	"github.com/wesishee/viatuta/internal/geo"
	"github.com/wesishee/viatuta/internal/graph"
)

// ErrUnroutable means no path exists under the given constraints.
var ErrUnroutable = errors.New("no route found")

// EdgeCostFunc returns the cost of traversing an edge, in "effective meters".
type EdgeCostFunc func(g *graph.Graph, edge int32) float64

// TurnCostFunc returns the cost of moving from one edge onto the next at
// their shared node.
type TurnCostFunc func(g *graph.Graph, in, out int32) float64

// AllowFunc is a hard filter. Returning false removes an edge from the graph
// entirely for this request.
type AllowFunc func(g *graph.Graph, edge int32) bool

// OverFunc reports whether an edge draws down the rider's stress budget —
// that is, whether it exceeds their stated comfort threshold.
type OverFunc func(g *graph.Graph, edge int32) bool

// Options configures one search.
type Options struct {
	EdgeCost EdgeCostFunc
	TurnCost TurnCostFunc // optional; nil means turns are free
	Allow    AllowFunc    // optional; nil means every edge is allowed

	// Over and BudgetM together implement the stress budget: edges Over
	// reports true for may be used, but their total length across the whole
	// route may not exceed BudgetM.
	//
	// Leaving Over nil or BudgetM at zero disables budget tracking entirely,
	// and the search collapses to ordinary Dijkstra.
	Over    OverFunc
	BudgetM float64

	// BudgetSteps is how finely the budget is tracked. See the note on
	// memory in Dijkstra; zero means the default.
	BudgetSteps int

	// MaxExpansions bounds the work a single request may do.
	MaxExpansions int

	// NoHeuristic disables A* and falls back to plain Dijkstra.
	//
	// The only reasons to set this are benchmarking the two against each
	// other, and cost functions that can return less than an edge's length
	// — which would break the heuristic's guarantee. The safety model's
	// multiplier floor of 1.0 means it never does.
	NoHeuristic bool
}

const (
	defaultMaxExpansions = 4_000_000
	defaultBudgetSteps   = 4
	maxBudgetSteps       = 16
)

// Path is a computed route.
type Path struct {
	Edges   []int32 // graph edge indices, in travel order
	Cost    float64 // total cost in effective meters
	LengthM float64 // true distance

	// BudgetUsedM is how much over-threshold road the route contains.
	BudgetUsedM float64

	Expansions int // states settled; a rough measure of search effort
}

// LengthOnlyCost is the distance-only cost function.
//
// It is the baseline every safety route is compared against, because the
// difference between the two is precisely what the safety model is buying and
// what detour it is charging for.
func LengthOnlyCost(g *graph.Graph, edge int32) float64 {
	return float64(g.LengthM[edge])
}

// Search finds the cheapest path from start to goal.
//
// # Search state
//
// The state is the directed EDGE you arrived on, not the node you are
// standing at, because a turn cost depends on both the edge you came from and
// the edge you are leaving on. A node-based search cannot express "turning
// left here is dangerous" at all.
//
// # A*
//
// The search is guided by straight-line distance to the goal. A* requires a
// heuristic that never OVERestimates the remaining cost, and the safety
// model supplies that guarantee structurally: every edge costs at least its
// own length, because the cost multiplier has a floor of 1.0. Straight-line
// distance is therefore always a valid underestimate of what is left to pay.
//
// The heuristic is also consistent, which is what lets the search settle each
// state exactly once. For any move from edge e onto edge f, the straight-line
// distance between their endpoints cannot exceed f's road length, which
// cannot exceed f's cost — so h(e) <= cost(e->f) + h(f) always holds.
//
// # The stress budget
//
// When a budget is in play the state gains a second dimension: how much
// over-threshold distance has been spent getting here. The same edge reached
// with 0 m of budget spent and with 350 m spent are genuinely different
// situations — the first can still afford a stressful crossing ahead, the
// second cannot — so they must be explored separately.
//
// Tracking that exactly is the resource-constrained shortest path problem,
// which is NP-hard. Discretising the budget into a handful of buckets makes
// it tractable: the search is exact with respect to the bucketed budget, and
// never exceeds the true budget, because the exact metres are carried
// alongside and checked before any state is created. The cost is that a route
// may be slightly more conservative than strictly necessary, by at most one
// bucket's width.
func Search(g *graph.Graph, start, goal int32, opt Options) (*Path, error) {
	if start == goal {
		return &Path{}, nil
	}
	if err := validate(g, start, goal); err != nil {
		return nil, err
	}

	maxExp := opt.MaxExpansions
	if maxExp <= 0 {
		maxExp = defaultMaxExpansions
	}

	// nb is the number of budget buckets. One bucket means no budget
	// tracking, and every array below collapses to the simple case.
	nb, stepM := budgetBuckets(opt)

	numEdges := int32(g.NumEdges())
	numStates := int(numEdges) * nb

	w := getWorkspace(numStates, g.NumNodes())
	defer w.release()

	dist, usedM, prev, done := w.dist, w.usedM, w.prev, w.done

	// h returns the heuristic for an edge: the straight-line distance from
	// where that edge ends to the goal. Zero disables the guidance, turning
	// the search back into plain Dijkstra.
	h := func(e int32) float64 { return 0 }
	if !opt.NoHeuristic {
		goalLat, goalLon := g.NodeLat[goal], g.NodeLon[goal]
		h = func(e int32) float64 {
			n := g.To[e]
			if v := w.hcache[n]; v >= 0 {
				return float64(v)
			}
			d := geo.DistanceM(
				geo.LatLon{Lat: g.NodeLat[n], Lon: g.NodeLon[n]},
				geo.LatLon{Lat: goalLat, Lon: goalLon})
			w.hcache[n] = float32(d)
			return d
		}
	}

	pq := &w.queue

	// Seed with every edge leaving the start node. There is no turn cost on
	// the first edge because the rider has not come from anywhere yet.
	lo, hi := g.OutEdges(start)
	for e := lo; e < hi; e++ {
		if opt.Allow != nil && !opt.Allow(g, e) {
			continue
		}
		used := 0.0
		if opt.Over != nil && opt.Over(g, e) {
			used = float64(g.LengthM[e])
			if used > opt.BudgetM {
				continue // this edge alone would blow the budget
			}
		}
		st := stateIndex(e, bucketOf(used, stepM, nb), nb)
		c := opt.EdgeCost(g, e)
		if c < dist[st] {
			dist[st] = c
			usedM[st] = float32(used)
			// Ordered by cost-so-far PLUS the estimate of what remains;
			// dist keeps the true cost, unpolluted by the estimate.
			pq.push(queueItem{state: st, priority: c + h(e)})
		}
	}

	expansions := 0
	for pq.Len() > 0 {
		item := pq.pop()
		st := item.state
		if done[st] {
			continue // a cheaper route to this state was found after queueing
		}
		done[st] = true

		expansions++
		if expansions > maxExp {
			return nil, ErrUnroutable
		}

		e := edgeOf(st, nb)
		if g.To[e] == goal {
			// Testing the goal on POP rather than on push is what makes the
			// answer optimal: a cheaper route to the goal may still be
			// sitting in the queue when a costlier one is first discovered.
			return buildPath(g, prev, st, nb, dist[st], float64(usedM[st]), expansions), nil
		}

		at := g.To[e]
		spent := float64(usedM[st])

		flo, fhi := g.OutEdges(at)
		for f := flo; f < fhi; f++ {
			if opt.Allow != nil && !opt.Allow(g, f) {
				continue
			}

			nextUsed := spent
			if opt.Over != nil && opt.Over(g, f) {
				nextUsed += float64(g.LengthM[f])
				// The exact metres are checked here, which is what makes
				// the budget a real bound rather than an approximation.
				if nextUsed > opt.BudgetM {
					continue
				}
			}

			nst := stateIndex(f, bucketOf(nextUsed, stepM, nb), nb)
			if done[nst] {
				continue
			}

			// dist[st], not item.priority: the priority carries the
			// heuristic, and adding to it would compound the estimate into
			// the cost at every step.
			nd := dist[st] + opt.EdgeCost(g, f)
			if opt.TurnCost != nil {
				nd += opt.TurnCost(g, e, f)
			}
			if nd < dist[nst] {
				dist[nst] = nd
				usedM[nst] = float32(nextUsed)
				prev[nst] = st
				pq.push(queueItem{state: nst, priority: nd + h(f)})
			}
		}
	}

	return nil, ErrUnroutable
}

// budgetBuckets decides how finely to track the budget.
//
// Memory is the constraint: the search allocates arrays of numEdges x buckets,
// so on a 400k-edge graph each bucket costs roughly 7 MB. Four buckets is
// enough resolution for a budget measured in hundreds of metres.
func budgetBuckets(opt Options) (nb int, stepM float64) {
	if opt.Over == nil || opt.BudgetM <= 0 {
		return 1, 0
	}
	nb = opt.BudgetSteps
	if nb <= 0 {
		nb = defaultBudgetSteps
	}
	if nb > maxBudgetSteps {
		nb = maxBudgetSteps
	}
	return nb, opt.BudgetM / float64(nb)
}

func stateIndex(edge int32, bucket, nb int) int32 { return edge*int32(nb) + int32(bucket) }
func edgeOf(state int32, nb int) int32            { return state / int32(nb) }

// bucketOf maps spent budget to its bucket, clamped to the last one.
func bucketOf(used, stepM float64, nb int) int {
	if nb == 1 || stepM <= 0 {
		return 0
	}
	b := int(used / stepM)
	if b >= nb {
		b = nb - 1
	}
	return b
}

func validate(g *graph.Graph, start, goal int32) error {
	n := int32(g.NumNodes())
	if start < 0 || start >= n || goal < 0 || goal >= n {
		return errors.New("routing: node index out of range")
	}
	if g.OutDegree(start) == 0 {
		return ErrUnroutable
	}
	return nil
}

// buildPath walks the predecessor chain back to the start.
func buildPath(g *graph.Graph, prev []int32, last int32, nb int,
	cost, used float64, expansions int) *Path {

	var rev []int32
	for st := last; st != -1; st = prev[st] {
		rev = append(rev, edgeOf(st, nb))
	}

	p := &Path{
		Edges:       make([]int32, len(rev)),
		Cost:        cost,
		BudgetUsedM: used,
		Expansions:  expansions,
	}
	for i, e := range rev {
		p.Edges[len(rev)-1-i] = e
		p.LengthM += float64(g.LengthM[e])
	}
	return p
}

// --- priority queue ------------------------------------------------------

type queueItem struct {
	state int32

	// priority is cost-so-far plus the heuristic estimate of what remains.
	// It orders the queue and is never mistaken for the route's cost, which
	// lives in dist.
	priority float64
}

// stateQueue is a min-heap of search states ordered by priority.
//
// The sift operations are written out rather than using container/heap, which
// takes `any` and therefore boxes every pushed item onto the heap — one
// allocation per push, and a search performs hundreds of thousands of them.
// Measured on a metro-sized graph, dropping the interface removed about
// 390,000 allocations and 6 MB per search.
type stateQueue []queueItem

func (q stateQueue) Len() int { return len(q) }

// push adds an item and restores the heap property.
func (q *stateQueue) push(item queueItem) {
	*q = append(*q, item)
	q.up(len(*q) - 1)
}

// pop removes and returns the lowest-priority item.
func (q *stateQueue) pop() queueItem {
	old := *q
	n := len(old) - 1
	top := old[0]
	old[0] = old[n]
	*q = old[:n]
	if n > 0 {
		q.down(0)
	}
	return top
}

func (q stateQueue) up(i int) {
	item := q[i]
	for i > 0 {
		parent := (i - 1) / 2
		if q[parent].priority <= item.priority {
			break
		}
		q[i] = q[parent]
		i = parent
	}
	q[i] = item
}

func (q stateQueue) down(i int) {
	n := len(q)
	item := q[i]
	for {
		left := 2*i + 1
		if left >= n {
			break
		}
		smallest := left
		if right := left + 1; right < n && q[right].priority < q[left].priority {
			smallest = right
		}
		if q[smallest].priority >= item.priority {
			break
		}
		q[i] = q[smallest]
		i = smallest
	}
	q[i] = item
}
