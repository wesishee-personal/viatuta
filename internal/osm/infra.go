package osm

import (
	"strings"

	"github.com/paulmach/osm"
)

// InfraClass is the cycling facility present on a way, best to worst.
// The values match the CHECK constraint on routing_edge.infra_class.
type InfraClass string

const (
	InfraProtectedTrack InfraClass = "protected_track" // physically separated
	InfraBufferedLane   InfraClass = "buffered_lane"   // paint plus a painted buffer
	InfraPaintedLane    InfraClass = "painted_lane"    // paint only
	InfraSharedLane     InfraClass = "shared_lane"     // sharrow: paint, no space
	InfraNone           InfraClass = "none"            // mixed traffic
	InfraPath           InfraClass = "path"            // off-street shared-use path
	InfraPedestrian     InfraClass = "pedestrian"      // footway where cycling is allowed
)

// rank orders on-street classes so the best facility on a way wins when
// several cycleway:* tags disagree. Off-street classes are not ranked: they
// describe the way itself rather than a facility attached to a road.
var rank = map[InfraClass]int{
	InfraNone:           0,
	InfraSharedLane:     1,
	InfraPaintedLane:    2,
	InfraBufferedLane:   3,
	InfraProtectedTrack: 4,
}

// excludedHighways are never routable for a cyclist, whatever else is tagged.
var excludedHighways = map[string]bool{
	"motorway": true, "motorway_link": true,
	"proposed": true, "construction": true, "planned": true,
	"raceway": true, "bus_guideway": true, "busway": true,
	"elevator": true, "platform": true, "corridor": true,
	"steps":     true, // carrying a bike up stairs is not a route
	"rest_area": true, "services": true,
}

// allowedHighways are routable by default, subject to access tags.
var allowedHighways = map[string]bool{
	"cycleway": true, "path": true, "track": true,
	"residential": true, "living_street": true, "unclassified": true,
	"service": true, "road": true,
	"tertiary": true, "tertiary_link": true,
	"secondary": true, "secondary_link": true,
	"primary": true, "primary_link": true,
	"trunk": true, "trunk_link": true,
}

// excludedServiceTypes are service roads a cyclist should never be routed
// along. Parking aisles and driveways together make up more than half of all
// service ways in a US extract, and routing through them is both unrealistic
// and actively unsafe — a parking aisle means reversing cars with poor
// sightlines, which is exactly what this project exists to avoid.
//
// Plain highway=service with no subtype is kept: those are usually legitimate
// access roads and alley connections.
var excludedServiceTypes = map[string]bool{
	"parking_aisle":    true,
	"driveway":         true,
	"drive-through":    true,
	"drive_through":    true,
	"emergency_access": true,
}

// permissionRequiredHighways are routable only when cycling is explicitly
// permitted. In the US a footway is closed to bikes unless signed otherwise,
// so assuming access would invent connections that do not legally exist.
var permissionRequiredHighways = map[string]bool{
	"footway": true, "pedestrian": true, "bridleway": true,
}

// Routable reports whether a cyclist may legally ride this way.
//
// Being conservative here matters in both directions. Including a way that is
// closed to bikes invents a route nobody can ride; excluding a legal one
// silently removes options and can push a rider onto a worse street.
func Routable(tags osm.Tags) bool {
	highway := tags.Find("highway")
	if highway == "" || excludedHighways[highway] {
		return false
	}

	bicycle := strings.ToLower(tags.Find("bicycle"))

	// An explicit bicycle prohibition always wins. "dismount" means you must
	// walk, which is not cycling, so it is excluded too.
	if bicycle == "no" || bicycle == "private" || bicycle == "dismount" || bicycle == "use_sidepath" {
		return false
	}

	// A general access restriction applies unless cycling is carved out.
	access := strings.ToLower(tags.Find("access"))
	bikeAllowed := bicycle == "yes" || bicycle == "designated" || bicycle == "permissive" || bicycle == "destination"
	if (access == "no" || access == "private") && !bikeAllowed {
		return false
	}

	if highway == "service" && excludedServiceTypes[strings.ToLower(tags.Find("service"))] {
		return false
	}

	if permissionRequiredHighways[highway] {
		return bikeAllowed
	}
	if allowedHighways[highway] {
		return true
	}
	return false
}

// ClassifyInfra determines which cycling facility a way carries.
func ClassifyInfra(tags osm.Tags) InfraClass {
	switch tags.Find("highway") {
	case "cycleway":
		// A way tagged highway=cycleway is drawn as its own line, which
		// means it is physically separate from the carriageway. That is the
		// definition of a protected track.
		return InfraProtectedTrack
	case "path", "track":
		return InfraPath
	case "footway", "pedestrian", "bridleway":
		return InfraPedestrian
	}

	// Otherwise this is a road, and any facility is described by cycleway:*
	// tags. Several may be present and disagree (a track on the right, paint
	// on the left); take the best, since a rider can use it in the direction
	// that matters.
	best := InfraNone
	for _, key := range []string{"cycleway", "cycleway:both", "cycleway:left", "cycleway:right"} {
		v := tags.Find(key)
		if v == "" {
			continue
		}
		if c := classifyCyclewayValue(v, key, tags); rank[c] > rank[best] {
			best = c
		}
	}
	return best
}

// classifyCyclewayValue interprets one cycleway:* value.
func classifyCyclewayValue(v, key string, tags osm.Tags) InfraClass {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "track", "opposite_track", "sidepath":
		return InfraProtectedTrack

	case "lane", "opposite_lane", "opposite":
		// A painted buffer or a physical separator upgrades a plain lane.
		// Riders consistently report buffered lanes as markedly less
		// stressful, and crash exposure drops with lateral distance.
		if hasBuffer(key, tags) {
			return InfraBufferedLane
		}
		return InfraPaintedLane

	case "shared_lane", "share_busway", "opposite_share_busway":
		// A sharrow is paint on a shared travel lane. It grants no space,
		// so it is barely better than nothing — hence the low rank.
		return InfraSharedLane

	case "separate":
		// The facility exists but is mapped as its own way, which we will
		// pick up separately. Claiming it here would double-count it.
		return InfraNone
	}
	return InfraNone
}

// hasBuffer reports whether a lane is buffered or separated from traffic,
// checking the side-specific tag before the generic one.
func hasBuffer(key string, tags osm.Tags) bool {
	for _, suffix := range []string{":buffer", ":separation"} {
		if v := tags.Find(key + suffix); v != "" && !isFalsy(v) && v != "no" {
			return true
		}
	}
	for _, k := range []string{"cycleway:buffer", "cycleway:both:buffer", "cycleway:separation"} {
		if v := tags.Find(k); v != "" && !isFalsy(v) {
			return true
		}
	}
	return false
}
