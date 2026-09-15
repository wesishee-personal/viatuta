package routing

import "github.com/wesishee/viatuta/internal/graph"

const (
	// AlternativePenalty multiplies the cost of edges already used by an
	// accepted route, pushing the next search toward genuinely different
	// roads rather than a near-identical variant.
	AlternativePenalty = 2.0

	// MaxOverlap is how much of a candidate's length may be shared with an
	// already-accepted route before it is considered the same route.
	MaxOverlap = 0.7

	// MaxCostRatio caps how much worse an alternative may be than the best
	// route. Offering a rider a route twice as bad is not a choice, it is
	// noise.
	MaxCostRatio = 1.6

	// maxAttempts bounds the searches run while hunting for distinct
	// options, so a request cannot spiral on a network with no real
	// alternatives.
	maxAttempts = 6
)

// Alternatives returns the best route plus up to n genuinely different ones.
//
// The method is iterative penalisation: find the best route, make its edges
// artificially expensive, search again, and keep the result if it is both
// meaningfully different and not much worse. This matters more here than in
// an ordinary router — when the safest route is a long detour, a rider
// deserves to see the trade rather than simply be sent the long way.
//
// The returned costs are the TRUE costs, recomputed without the penalty, so
// they can be compared with each other and with the primary route.
func Alternatives(g *graph.Graph, start, goal int32, opt Options, n int) ([]*Path, error) {
	primary, err := Search(g, start, goal, opt)
	if err != nil {
		return nil, err
	}
	routes := []*Path{primary}
	if n <= 0 || len(primary.Edges) == 0 {
		return routes, nil
	}

	// penalised marks every edge used by an accepted route.
	penalised := make(map[int32]bool, len(primary.Edges)*2)
	markUsed(g, primary, penalised)

	baseCost := opt.EdgeCost
	altOpt := opt
	altOpt.EdgeCost = func(g *graph.Graph, e int32) float64 {
		c := baseCost(g, e)
		if penalised[e] {
			c *= AlternativePenalty
		}
		return c
	}

	for attempt := 0; attempt < maxAttempts && len(routes) < n+1; attempt++ {
		cand, err := Search(g, start, goal, altOpt)
		if err != nil || len(cand.Edges) == 0 {
			break
		}

		// Score the candidate with the real cost function, not the penalised
		// one, or every alternative would look far worse than it is.
		trueCost := scorePath(g, opt, cand.Edges)

		if trueCost > primary.Cost*MaxCostRatio {
			break // and every later attempt will be worse still
		}
		if !isDistinct(g, cand, routes) {
			// Penalise this attempt's edges too, so the next search is
			// pushed somewhere new rather than rediscovering the same path.
			markUsed(g, cand, penalised)
			continue
		}

		cand.Cost = trueCost
		routes = append(routes, cand)
		markUsed(g, cand, penalised)
	}

	return routes, nil
}

// markUsed records a route's edges, and their reverses, as used.
//
// Including the reverse direction matters: an "alternative" that runs back
// down the other side of the same street is not a different route to a rider.
func markUsed(g *graph.Graph, p *Path, set map[int32]bool) {
	for _, e := range p.Edges {
		set[e] = true
		lo, hi := g.OutEdges(g.To[e])
		for f := lo; f < hi; f++ {
			if g.IsReverseOf(e, f) {
				set[f] = true
			}
		}
	}
}

// isDistinct reports whether a candidate differs enough from every accepted
// route to be worth offering.
func isDistinct(g *graph.Graph, cand *Path, accepted []*Path) bool {
	candLen := cand.LengthM
	if candLen <= 0 {
		return false
	}

	for _, r := range accepted {
		inR := make(map[int32]bool, len(r.Edges))
		for _, e := range r.Edges {
			inR[e] = true
		}

		var shared float64
		for _, e := range cand.Edges {
			if inR[e] {
				shared += float64(g.LengthM[e])
			}
		}
		if shared/candLen > MaxOverlap {
			return false
		}
	}
	return true
}

// scorePath recomputes a route's cost under the unmodified cost functions.
func scorePath(g *graph.Graph, opt Options, edges []int32) float64 {
	var total float64
	for i, e := range edges {
		total += opt.EdgeCost(g, e)
		if i > 0 && opt.TurnCost != nil {
			total += opt.TurnCost(g, edges[i-1], e)
		}
	}
	return total
}
