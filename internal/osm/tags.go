// Package osm turns an OpenStreetMap extract into the routable graph stored
// in Postgres.
//
// OSM tagging is contributor-authored free text with conventions rather than
// rules, so this package is where messy reality is converted into the small
// set of typed attributes the safety model depends on. When the router later
// says a street is dangerous, the claim ultimately rests on the decisions
// made here — which is why almost every rule below cites the tag it reads.
package osm

import (
	"math"
	"strconv"
	"strings"

	"github.com/paulmach/osm"
)

// WayAttrs is everything the safety model needs from a single OSM way.
type WayAttrs struct {
	Highway string
	Name    string
	Infra   InfraClass
	Surface string

	// Pointers distinguish "tagged as this value" from "not tagged at all".
	// Unknown is not the same as absent: an unlit street and a street whose
	// lighting nobody has surveyed deserve different treatment.
	MaxSpeedMPH *int16
	Lanes       *int16
	Lit         *bool

	// Whether a cyclist may travel along the way in each direction.
	// Both are true for an ordinary two-way street.
	Forward  bool
	Backward bool
}

// truthy and falsy list the values OSM uses for boolean-ish tags. There is no
// single convention, so all the common spellings are accepted.
var (
	truthy = map[string]bool{"yes": true, "true": true, "1": true, "designated": true, "official": true}
	falsy  = map[string]bool{"no": true, "false": true, "0": true}
)

func isTruthy(v string) bool { return truthy[strings.ToLower(strings.TrimSpace(v))] }
func isFalsy(v string) bool  { return falsy[strings.ToLower(strings.TrimSpace(v))] }

// parseTriBool returns nil when the tag is absent or unrecognised.
func parseTriBool(v string) *bool {
	if isTruthy(v) {
		t := true
		return &t
	}
	if isFalsy(v) {
		f := false
		return &f
	}
	return nil
}

// parseMaxSpeedMPH normalises OSM's maxspeed tag to miles per hour.
//
// OSM's convention is that a bare number is km/h and an explicit unit
// overrides that. US data is usually tagged "35 mph", but both forms appear
// even within one city, so both must be handled or half of Austin's arterials
// would be read as 35 km/h — about 22 mph — and scored far too safe.
func parseMaxSpeedMPH(v string) *int16 {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return nil
	}
	// Some ways carry several values ("30 mph;40 mph") or a conditional
	// suffix. Take the first, which is the general case.
	if i := strings.IndexAny(v, ";@"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	// Non-numeric legal defaults carry no usable number.
	if v == "none" || v == "signals" || v == "walk" || strings.HasPrefix(v, "variable") {
		return nil
	}

	isMPH := strings.Contains(v, "mph")
	v = strings.TrimSpace(strings.NewReplacer("mph", "", "km/h", "", "kmh", "", "kph", "").Replace(v))

	n, err := strconv.ParseFloat(strings.Fields(v + " ")[0], 64)
	if err != nil || n <= 0 {
		return nil
	}
	if !isMPH {
		n *= 0.621371 // km/h -> mph
	}
	if n > 100 {
		return nil // implausible; almost certainly a tagging error
	}
	mph := int16(math.Round(n))
	return &mph
}

// parseLanes reads the lanes tag, rejecting implausible values.
func parseLanes(v string) *int16 {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if i := strings.Index(v, ";"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 || f > 12 {
		return nil
	}
	n := int16(math.Round(f))
	return &n
}

// directions decides whether a cyclist may ride each way along the way.
//
// This is deliberately separate from motor-vehicle direction. A one-way
// street very often permits contraflow cycling, and treating those as
// one-way for bikes would discard exactly the low-stress connections a
// safety-first router most wants to use.
func directions(tags osm.Tags) (forward, backward bool) {
	forward, backward = true, true

	switch v := strings.ToLower(tags.Find("oneway")); {
	case isTruthy(v):
		backward = false
	case v == "-1" || v == "reverse":
		forward = false
	}

	// Roundabouts are implicitly one-way even when untagged.
	if tags.Find("junction") == "roundabout" || tags.Find("junction") == "circular" {
		backward = false
	}

	// Bicycle-specific overrides win over the general tag.
	if v := tags.Find("oneway:bicycle"); v != "" {
		switch {
		case isFalsy(v):
			forward, backward = true, true
		case isTruthy(v):
			forward, backward = true, false
		case v == "-1" || v == "reverse":
			forward, backward = false, true
		}
	}

	// The legacy way of saying "contraflow cycling is allowed here".
	cw := tags.Find("cycleway")
	if strings.HasPrefix(cw, "opposite") {
		forward, backward = true, true
	}

	// A way that permits neither direction is not routable; callers filter
	// those out via Routable.
	return forward, backward
}

// ParseWay extracts the attributes the safety model needs from a way's tags.
func ParseWay(tags osm.Tags) WayAttrs {
	fwd, bwd := directions(tags)
	return WayAttrs{
		Highway:     tags.Find("highway"),
		Name:        tags.Find("name"),
		Infra:       ClassifyInfra(tags),
		Surface:     strings.ToLower(strings.TrimSpace(tags.Find("surface"))),
		MaxSpeedMPH: parseMaxSpeedMPH(tags.Find("maxspeed")),
		Lanes:       parseLanes(tags.Find("lanes")),
		Lit:         parseTriBool(tags.Find("lit")),
		Forward:     fwd,
		Backward:    bwd,
	}
}

// NodeControl maps a node's tags to the traffic-control value stored on
// routing_node. Phase 4's turn costs depend on this: crossing a busy road is
// far safer at a signal than at an uncontrolled junction.
func NodeControl(tags osm.Tags) string {
	switch tags.Find("highway") {
	case "traffic_signals":
		return "signal"
	case "stop":
		return "stop"
	case "give_way":
		return "yield"
	case "crossing":
		return "crossing"
	}
	// A node can also be tagged only as a crossing.
	if tags.Find("crossing") != "" || tags.Find("footway") == "crossing" {
		if isTruthy(tags.Find("crossing:signals")) || tags.Find("crossing") == "traffic_signals" {
			return "signal"
		}
		return "crossing"
	}
	return "none"
}
