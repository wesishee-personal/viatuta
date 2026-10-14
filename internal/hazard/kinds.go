package hazard

import (
	"time"
)

// Kind describes one type of hazard a rider can report.
//
// This registry is the single source of truth for what the kinds are and how
// long they live. The CHECK constraint on hazard_report.kind must list the
// same names (a test enforces it), and clients read the whole table from
// GET /v1/hazards/kinds instead of hard-coding it, so adding a kind does not
// need an app release.
type Kind struct {
	Name  string
	Label string

	// Weight is the kind's influence on routing, 0..1. It is the starting
	// point for tuning, not a measurement.
	Weight float64

	// TTL is how long a fresh report lives. A hazard that is no longer there
	// stops mattering within hours (an accident) or weeks (a pothole), and
	// leaving it up would teach riders to ignore the map.
	TTL time.Duration

	// ConfirmExtension is how much life each confirmation adds, and MaxLife
	// caps the total lifetime however many confirmations arrive. Without the
	// cap a stream of confirmations could keep a one-day closure alive for
	// months.
	ConfirmExtension time.Duration
	MaxLife          time.Duration

	// MergeRadiusM is how close a second report of the same kind must be to
	// count as the same hazard. Area hazards (an accident, a flooded
	// underpass) are fuzzier than place-specific ones (one pothole), so they
	// merge over a wider radius.
	MergeRadiusM float64

	// Directional kinds affect riders travelling one way (a pothole in the
	// eastbound lane). Others affect both directions of the road.
	Directional bool

	// Closure kinds say the road cannot be ridden, which is a different claim
	// from "this stretch is unpleasant". They bypass the ordinary hazard score
	// (a gentle nudge) and use the closure penalty, which grows steeply with
	// the number of riders reporting it. See ClosurePenalty.
	Closure bool
}

const (
	hour = time.Hour
	day  = 24 * time.Hour

	// Merge radii. See Kind.MergeRadiusM.
	areaMergeM  = 50
	placeMergeM = 25
)

// kinds lists every hazard kind, in the order clients should display them.
var kinds = []Kind{
	{Name: "accident", Label: "Accident", Weight: 0.9,
		TTL: 3 * hour, ConfirmExtension: 1 * hour, MaxLife: 8 * hour,
		MergeRadiusM: areaMergeM, Directional: true},
	{Name: "road_closed", Label: "Road closed", Weight: 1.0,
		TTL: 1 * day, ConfirmExtension: 12 * hour, MaxLife: 7 * day,
		MergeRadiusM: areaMergeM, Closure: true},
	{Name: "construction", Label: "Road work", Weight: 0.9,
		TTL: 1 * day, ConfirmExtension: 12 * hour, MaxLife: 7 * day,
		MergeRadiusM: areaMergeM},
	{Name: "flooding", Label: "Flooding", Weight: 0.8,
		TTL: 12 * hour, ConfirmExtension: 6 * hour, MaxLife: 48 * hour,
		MergeRadiusM: areaMergeM},
	{Name: "poor_visibility", Label: "Poor visibility", Weight: 0.6,
		TTL: 12 * hour, ConfirmExtension: 6 * hour, MaxLife: 3 * day,
		MergeRadiusM: areaMergeM, Directional: true}, // glare depends on which way you face
	{Name: "aggressive_traffic", Label: "Aggressive traffic", Weight: 0.7,
		TTL: 6 * hour, ConfirmExtension: 3 * hour, MaxLife: 1 * day,
		MergeRadiusM: areaMergeM, Directional: true},
	{Name: "signal_out", Label: "Traffic signal out", Weight: 0.6,
		TTL: 4 * hour, ConfirmExtension: 2 * hour, MaxLife: 1 * day,
		MergeRadiusM: areaMergeM},
	{Name: "street_event", Label: "Street event", Weight: 0.7,
		TTL: 12 * hour, ConfirmExtension: 6 * hour, MaxLife: 3 * day,
		MergeRadiusM: areaMergeM},
	{Name: "dangerous_junction", Label: "Dangerous junction", Weight: 1.0,
		TTL: 30 * day, ConfirmExtension: 30 * day, MaxLife: 180 * day,
		MergeRadiusM: areaMergeM},
	{Name: "pothole", Label: "Pothole", Weight: 0.5,
		TTL: 7 * day, ConfirmExtension: 7 * day, MaxLife: 60 * day,
		MergeRadiusM: placeMergeM, Directional: true},
	{Name: "glass", Label: "Broken glass", Weight: 0.6,
		TTL: 3 * day, ConfirmExtension: 3 * day, MaxLife: 14 * day,
		MergeRadiusM: placeMergeM, Directional: true},
	{Name: "debris", Label: "Debris", Weight: 0.4,
		TTL: 2 * day, ConfirmExtension: 2 * day, MaxLife: 14 * day,
		MergeRadiusM: placeMergeM, Directional: true},
	{Name: "slippery_surface", Label: "Slippery surface", Weight: 0.7,
		TTL: 12 * hour, ConfirmExtension: 6 * hour, MaxLife: 3 * day,
		MergeRadiusM: placeMergeM, Directional: true},
	{Name: "blocked_lane", Label: "Blocked lane", Weight: 0.8,
		TTL: 3 * hour, ConfirmExtension: 2 * hour, MaxLife: 12 * hour,
		MergeRadiusM: placeMergeM, Directional: true},
	{Name: "fallen_tree", Label: "Fallen tree", Weight: 0.8,
		TTL: 1 * day, ConfirmExtension: 12 * hour, MaxLife: 7 * day,
		MergeRadiusM: placeMergeM},
	{Name: "loose_animal", Label: "Loose animal", Weight: 0.3,
		TTL: 1 * hour, ConfirmExtension: 30 * time.Minute, MaxLife: 4 * hour,
		MergeRadiusM: placeMergeM},
	{Name: "other", Label: "Other", Weight: 0.3,
		TTL: 1 * day, ConfirmExtension: 12 * hour, MaxLife: 7 * day,
		MergeRadiusM: placeMergeM},
}

var kindsByName = func() map[string]Kind {
	m := make(map[string]Kind, len(kinds))
	for _, k := range kinds {
		m[k.Name] = k
	}
	return m
}()

// Lookup returns the kind with the given name.
func Lookup(name string) (Kind, bool) {
	k, ok := kindsByName[name]
	return k, ok
}

// All returns every kind in display order. The slice is a copy.
func All() []Kind {
	return append([]Kind(nil), kinds...)
}

// Names returns every kind name, sorted for stable error messages.
func Names() []string {
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, k.Name)
	}
	// Insertion sort: tiny list, no import needed.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
