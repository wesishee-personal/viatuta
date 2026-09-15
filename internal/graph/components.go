package graph

import "sort"

// ComponentStats summarises how the graph breaks into disconnected pieces.
type ComponentStats struct {
	Count       int     // number of connected components
	LargestSize int     // nodes in the biggest component
	LargestPct  float64 // that size as a percentage of all nodes
	Isolated    int     // components of a single node
	TopSizes    []int   // the ten largest, for eyeballing
}

// WeaklyConnectedComponents groups nodes that are connected when edge
// direction is ignored.
//
// This is the first sanity check on a freshly ingested graph. If the network
// has shattered into thousands of islands, routing will fail in ways that
// look like search bugs but are really data bugs — so it is worth knowing
// before building a router on top.
//
// Direction is ignored deliberately: a graph that is weakly connected but not
// strongly connected has a one-way problem, while one that is not even weakly
// connected has a far more basic gap in the data.
func (g *Graph) WeaklyConnectedComponents() ComponentStats {
	return g.componentsWhere(nil)
}

// ComponentsUpToLTS reports connectivity using only edges at or below a
// traffic-stress limit — that is, the network as a given rider profile
// actually sees it.
//
// This is the honest measure of whether a profile is usable. A MaxLTS that
// shatters the network into islands will produce "unroutable" for ordinary
// trips no matter how good the search is, and that is a property of the city
// and the threshold, not a bug.
func (g *Graph) ComponentsUpToLTS(maxLTS uint8) ComponentStats {
	return g.componentsWhere(func(e int32) bool {
		lts := g.LTS[e]
		return lts == 0 || lts <= maxLTS
	})
}

// componentsWhere computes components over the subgraph that keep returns
// true for. A nil predicate uses every edge.
func (g *Graph) componentsWhere(keep func(int32) bool) ComponentStats {
	uf := newUnionFind(g.NumNodes())
	for i := range g.From {
		if keep != nil && !keep(int32(i)) {
			continue
		}
		uf.union(g.From[i], g.To[i])
	}

	sizes := make(map[int32]int, 1024)
	for n := int32(0); n < int32(g.NumNodes()); n++ {
		sizes[uf.find(n)]++
	}

	stats := ComponentStats{Count: len(sizes)}
	all := make([]int, 0, len(sizes))
	for _, s := range sizes {
		all = append(all, s)
		if s == 1 {
			stats.Isolated++
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(all)))

	if len(all) > 0 {
		stats.LargestSize = all[0]
		stats.LargestPct = 100 * float64(all[0]) / float64(g.NumNodes())
	}
	if len(all) > 10 {
		all = all[:10]
	}
	stats.TopSizes = all
	return stats
}

// LargestComponent returns a mask marking the nodes in the biggest weakly
// connected component.
//
// Phase 3 uses this to reject a request whose endpoints lie in different
// components before running a search that could never succeed.
func (g *Graph) LargestComponent() []bool {
	uf := newUnionFind(g.NumNodes())
	for i := range g.From {
		uf.union(g.From[i], g.To[i])
	}

	sizes := make(map[int32]int, 1024)
	var best int32
	var bestSize int
	for n := int32(0); n < int32(g.NumNodes()); n++ {
		r := uf.find(n)
		sizes[r]++
		if sizes[r] > bestSize {
			bestSize, best = sizes[r], r
		}
	}

	mask := make([]bool, g.NumNodes())
	for n := int32(0); n < int32(g.NumNodes()); n++ {
		mask[n] = uf.find(n) == best
	}
	return mask
}

// unionFind is the standard disjoint-set structure with path compression and
// union by rank, which makes each operation effectively constant time.
type unionFind struct {
	parent []int32
	rank   []uint8
}

func newUnionFind(n int) *unionFind {
	uf := &unionFind{parent: make([]int32, n), rank: make([]uint8, n)}
	for i := range uf.parent {
		uf.parent[i] = int32(i)
	}
	return uf
}

func (uf *unionFind) find(x int32) int32 {
	for uf.parent[x] != x {
		// Path halving: point each node at its grandparent as we walk up,
		// which flattens the tree without a second pass.
		uf.parent[x] = uf.parent[uf.parent[x]]
		x = uf.parent[x]
	}
	return x
}

func (uf *unionFind) union(a, b int32) {
	ra, rb := uf.find(a), uf.find(b)
	if ra == rb {
		return
	}
	if uf.rank[ra] < uf.rank[rb] {
		ra, rb = rb, ra
	}
	uf.parent[rb] = ra
	if uf.rank[ra] == uf.rank[rb] {
		uf.rank[ra]++
	}
}
