package safety

import "fmt"

// Profile is a rider's tolerance for risk, expressed as weights on each term
// of the cost function plus hard constraints.
//
// Weights are multipliers on the penalties in params.go. A weight of 0
// disables a term entirely; 1.0 applies the documented penalty as-is.
type Profile struct {
	Name string `json:"name"`

	// MaxLTS is the stress level this rider is comfortable riding.
	MaxLTS uint8 `json:"max_lts"`

	// StressBudgetM is how many meters ABOVE MaxLTS the rider will accept
	// across the whole route.
	//
	// This exists because a hard threshold is unusable in practice: Austin's
	// low-stress network is broken into islands by arterials, so LTS <= 2
	// reaches under a fifth of the city. Riders bridge those islands by
	// taking one bad block — not ten. The budget encodes exactly that, and
	// is enforced as a real bound, not a preference.
	//
	// Zero restores the hard filter: over-threshold edges become
	// untraversable and the router reports "unroutable" rather than using
	// them at all.
	StressBudgetM float64 `json:"stress_budget_m"`

	WeightLTS     float64 `json:"weight_lts"`
	WeightCrash   float64 `json:"weight_crash"`
	WeightGrade   float64 `json:"weight_grade"`
	WeightSurface float64 `json:"weight_surface"`
	WeightLight   float64 `json:"weight_light"`
	WeightTurn    float64 `json:"weight_turn"`
	WeightHazard  float64 `json:"weight_hazard"`

	// MaxDetourRatio is how much longer than the shortest possible route
	// this rider will accept before the result is worth flagging.
	MaxDetourRatio float64 `json:"max_detour_ratio"`

	// Night enables the lighting term.
	Night bool `json:"night"`
}

// The built-in profiles. These are defaults, not limits: the API accepts a
// full custom weight object.
var (
	// Cautious is riding with children. It will accept a long detour for
	// physical separation and refuses anything above LTS 2.
	Cautious = Profile{
		Name: "cautious", MaxLTS: 2, StressBudgetM: 400,
		WeightLTS: 1.5, WeightCrash: 1.2, WeightGrade: 1.0,
		WeightSurface: 1.2, WeightLight: 1.0, WeightTurn: 1.5, WeightHazard: 1.5,
		MaxDetourRatio: 2.5,
	}

	// Comfortable is the default: prefers facilities, tolerates quiet
	// streets, avoids arterials without a bike lane.
	Comfortable = Profile{
		Name: "comfortable", MaxLTS: 3, StressBudgetM: 800,
		WeightLTS: 1.0, WeightCrash: 1.0, WeightGrade: 1.0,
		WeightSurface: 1.0, WeightLight: 1.0, WeightTurn: 1.0, WeightHazard: 1.0,
		MaxDetourRatio: 1.8,
	}

	// Confident will take a bike lane on an arterial to save real time.
	Confident = Profile{
		Name: "confident", MaxLTS: 4, StressBudgetM: 2000,
		WeightLTS: 0.35, WeightCrash: 0.6, WeightGrade: 0.5,
		WeightSurface: 0.6, WeightLight: 0.5, WeightTurn: 0.5, WeightHazard: 0.6,
		MaxDetourRatio: 1.35,
	}

	// Shortest is the distance-only baseline. Every safety route is compared
	// against it, because the gap between the two is exactly what the safety
	// model is buying and what detour it is charging for.
	Shortest = Profile{
		Name: "shortest", MaxLTS: 4,
		MaxDetourRatio: 1.0,
	}
)

// ProfileByName looks up a built-in profile.
func ProfileByName(name string) (Profile, error) {
	switch name {
	case "cautious":
		return Cautious, nil
	case "comfortable", "":
		return Comfortable, nil
	case "confident":
		return Confident, nil
	case "shortest":
		return Shortest, nil
	default:
		return Profile{}, fmt.Errorf("unknown profile %q (want cautious, comfortable, confident or shortest)", name)
	}
}

// Validate checks a caller-supplied profile for values that would break the
// cost model's guarantees.
func (p Profile) Validate() error {
	if p.MaxLTS < 1 || p.MaxLTS > 4 {
		return fmt.Errorf("max_lts must be between 1 and 4, got %d", p.MaxLTS)
	}
	// A negative weight would make some edges cheaper than their own length,
	// which breaks the floor the A* heuristic depends on in Phase 7.
	for name, w := range map[string]float64{
		"weight_lts": p.WeightLTS, "weight_crash": p.WeightCrash,
		"weight_grade": p.WeightGrade, "weight_surface": p.WeightSurface,
		"weight_light": p.WeightLight, "weight_turn": p.WeightTurn,
		"weight_hazard": p.WeightHazard,
	} {
		if w < 0 {
			return fmt.Errorf("%s must not be negative, got %v", name, w)
		}
		if w > 10 {
			return fmt.Errorf("%s must be at most 10, got %v", name, w)
		}
	}
	if p.MaxDetourRatio < 1 || p.MaxDetourRatio > 10 {
		return fmt.Errorf("max_detour_ratio must be between 1 and 10, got %v", p.MaxDetourRatio)
	}
	if p.StressBudgetM < 0 || p.StressBudgetM > 20000 {
		return fmt.Errorf("stress_budget_m must be between 0 and 20000, got %v", p.StressBudgetM)
	}
	return nil
}
