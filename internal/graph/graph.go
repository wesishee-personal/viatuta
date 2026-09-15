// Package graph holds the in-memory routing graph.
//
// The graph is loaded from Postgres once at startup and then never touched by
// the database again during a search. A route request explores tens of
// thousands of edges; doing that over SQL round trips would be thousands of
// times slower than reading from RAM.
//
// Geometry is deliberately NOT stored here. The router only needs topology
// and cost attributes, which is about 40 bytes per edge; the shape of each
// road is an order of magnitude larger and is only needed for the handful of
// edges in the final answer, so it is fetched from Postgres at the end.
package graph

import "fmt"

// Infra is a cycling facility class, ordered worst to best so that
// comparisons like `>= InfraPaintedLane` read naturally.
type Infra uint8

const (
	InfraNone Infra = iota
	InfraPedestrian
	InfraSharedLane
	InfraPaintedLane
	InfraBufferedLane
	InfraPath
	InfraProtectedTrack
)

// String returns the database's text value for an Infra.
//
// The inverse of ParseInfra, and the single place these names are spelled —
// anything rendering a facility type to a client goes through here rather
// than keeping its own copy of the mapping to drift out of step.
func (i Infra) String() string {
	switch i {
	case InfraProtectedTrack:
		return "protected_track"
	case InfraPath:
		return "path"
	case InfraBufferedLane:
		return "buffered_lane"
	case InfraPaintedLane:
		return "painted_lane"
	case InfraSharedLane:
		return "shared_lane"
	case InfraPedestrian:
		return "pedestrian"
	default:
		return "none"
	}
}

// ParseInfra converts the database's text value to the compact form.
func ParseInfra(s string) Infra {
	switch s {
	case "protected_track":
		return InfraProtectedTrack
	case "path":
		return InfraPath
	case "buffered_lane":
		return InfraBufferedLane
	case "painted_lane":
		return InfraPaintedLane
	case "shared_lane":
		return InfraSharedLane
	case "pedestrian":
		return InfraPedestrian
	default:
		return InfraNone
	}
}

// Highway is a road class, compacted from OSM's string tag. Phase 4's LTS
// classification leans on this heavily, because the speed limit is missing on
// roughly three quarters of edges and road class is the best proxy available.
type Highway uint8

const (
	HwyOther Highway = iota
	HwyCycleway
	HwyPath
	HwyFootway
	HwyTrack
	HwyPedestrian
	HwyLivingStreet
	HwyService
	HwyResidential
	HwyUnclassified
	HwyTertiary
	HwySecondary
	HwyPrimary
	HwyTrunk
)

func ParseHighway(s string) Highway {
	switch s {
	case "cycleway":
		return HwyCycleway
	case "path":
		return HwyPath
	case "footway":
		return HwyFootway
	case "track":
		return HwyTrack
	case "pedestrian":
		return HwyPedestrian
	case "living_street":
		return HwyLivingStreet
	case "service":
		return HwyService
	case "residential":
		return HwyResidential
	case "unclassified":
		return HwyUnclassified
	case "tertiary", "tertiary_link":
		return HwyTertiary
	case "secondary", "secondary_link":
		return HwySecondary
	case "primary", "primary_link":
		return HwyPrimary
	case "trunk", "trunk_link":
		return HwyTrunk
	default:
		return HwyOther
	}
}

// Surface is a road surface class. Only the distinctions that change how a
// bike rides are kept; OSM has dozens of values with a long tail of rare ones.
type Surface uint8

const (
	SurfaceUnknown   Surface = iota
	SurfacePaved             // asphalt, concrete — the assumption for most streets
	SurfaceCompacted         // fine gravel, compacted stone; rideable but slower
	SurfaceLoose             // gravel, dirt, ground; a road bike will struggle
	SurfaceBad               // sand, mud; effectively unrideable
)

func ParseSurface(s string) Surface {
	switch s {
	case "asphalt", "concrete", "paved", "paving_stones", "chipseal", "concrete:plates":
		return SurfacePaved
	case "compacted", "fine_gravel", "gravel:fine", "pebblestone":
		return SurfaceCompacted
	case "gravel", "dirt", "ground", "earth", "grass", "unpaved", "cobblestone", "sett", "wood":
		return SurfaceLoose
	case "sand", "mud":
		return SurfaceBad
	default:
		return SurfaceUnknown
	}
}

// Control is how traffic is managed at a node.
type Control uint8

const (
	ControlNone Control = iota
	ControlSignal
	ControlStop
	ControlYield
	ControlCrossing
	ControlRoundabout
)

func ParseControl(s string) Control {
	switch s {
	case "signal":
		return ControlSignal
	case "stop":
		return ControlStop
	case "yield":
		return ControlYield
	case "crossing":
		return ControlCrossing
	case "roundabout":
		return ControlRoundabout
	default:
		return ControlNone
	}
}

// Unknown sentinels for attributes that are frequently untagged. A separate
// sentinel keeps "not surveyed" distinct from a real value — the distinction
// the safety model depends on.
const (
	SpeedUnknown int16 = -1
	LanesUnknown int8  = -1
	LitUnknown   int8  = -1
	LitNo        int8  = 0
	LitYes       int8  = 1
)

// Graph is a directed graph in compressed-sparse-row form.
//
// Rather than a map from node to a slice of edges — which would mean millions
// of small allocations and pointer chasing — every edge lives in one flat
// array sorted by its source node. The edges leaving node n are the
// contiguous range Offs[n]..Offs[n+1], which is both compact and friendly to
// the CPU cache.
//
// Attributes are stored as parallel arrays rather than a slice of structs so
// that a scan touching only LengthM does not drag every other field through
// the cache with it.
type Graph struct {
	// --- nodes ---
	NodeLat     []float64
	NodeLon     []float64
	NodeControl []Control

	// NodeMaxLTS is the highest traffic stress among all edges meeting at a
	// node — in effect, how busy the road you are crossing is. Computed once
	// after load; turn costs scale with it.
	NodeMaxLTS []uint8

	// --- edges, sorted by From ---
	From    []int32
	To      []int32
	LengthM []float32
	DBID    []int64 // routing_edge.id, for fetching geometry later

	Infra      []Infra
	Highway    []Highway
	Surface    []Surface
	MaxSpeed   []int16 // mph, SpeedUnknown when untagged
	Lanes      []int8  // LanesUnknown when untagged
	Lit        []int8  // LitUnknown / LitNo / LitYes
	LTS        []uint8 // 0 until `viatuta score-edges` computes it
	CrashScore []float32
	GradePct   []float32

	// Compass bearings in the direction of travel, for turn classification.
	BearingStart []float32
	BearingEnd   []float32

	// Offs has NumNodes+1 entries; Offs[n]..Offs[n+1] is node n's out-edges.
	Offs []int32

	// dbIndex maps routing_edge.id back to a graph edge index.
	//
	// These are NOT the same number. Edges are re-sorted by source node when
	// the graph is loaded, so anything computed in SQL and keyed by the
	// database id must be translated before it can index a graph array.
	// Skipping that translation silently attributes data to the wrong roads.
	dbIndex []int32
}

func (g *Graph) NumNodes() int { return len(g.NodeLat) }
func (g *Graph) NumEdges() int { return len(g.From) }

// OutEdges returns the half-open range of edge indices leaving node n.
//
// Because edges are sorted by source node, the range indices ARE the edge
// indices — no indirection table is needed.
func (g *Graph) OutEdges(n int32) (start, end int32) {
	return g.Offs[n], g.Offs[n+1]
}

// OutDegree reports how many edges leave node n.
func (g *Graph) OutDegree(n int32) int32 { return g.Offs[n+1] - g.Offs[n] }

// ComputeNodeMaxLTS fills NodeMaxLTS from the current edge LTS values.
//
// Both endpoints of an edge are updated, so a node records the stress of
// every road touching it regardless of direction — which is what "how bad is
// the road I am crossing here" actually means.
func (g *Graph) ComputeNodeMaxLTS() {
	g.NodeMaxLTS = make([]uint8, g.NumNodes())
	for e := range g.From {
		lts := g.LTS[e]
		if lts > g.NodeMaxLTS[g.From[e]] {
			g.NodeMaxLTS[g.From[e]] = lts
		}
		if lts > g.NodeMaxLTS[g.To[e]] {
			g.NodeMaxLTS[g.To[e]] = lts
		}
	}
}

// IndexOfDBID translates a routing_edge.id into a graph edge index.
func (g *Graph) IndexOfDBID(dbID int64) (int32, bool) {
	if dbID < 0 || dbID >= int64(len(g.dbIndex)) {
		return 0, false
	}
	i := g.dbIndex[dbID]
	if i < 0 {
		return 0, false
	}
	return i, true
}

// buildDBIndex builds the database-id to graph-index lookup.
//
// The ingest assigns edge ids densely from zero, which lets this be a flat
// array rather than a map — a few megabytes instead of tens. That assumption
// is checked rather than trusted: a sparse id space would silently produce a
// huge allocation.
func (g *Graph) buildDBIndex() error {
	var max int64 = -1
	for _, id := range g.DBID {
		if id > max {
			max = id
		}
	}
	if max < 0 {
		return nil
	}
	if max >= int64(len(g.DBID))*4 {
		return fmt.Errorf(
			"graph: routing_edge ids are sparse (max %d for %d edges); re-run the ingest",
			max, len(g.DBID))
	}

	g.dbIndex = make([]int32, max+1)
	for i := range g.dbIndex {
		g.dbIndex[i] = -1
	}
	for i, id := range g.DBID {
		g.dbIndex[id] = int32(i)
	}
	return nil
}

// IsReverseOf reports whether two edges are the same stretch of road in
// opposite directions — that is, whether taking b after a is a u-turn.
func (g *Graph) IsReverseOf(a, b int32) bool {
	return g.From[a] == g.To[b] && g.To[a] == g.From[b]
}
