package hazard

import (
	"github.com/wesishee/viatuta/internal/geo"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestRegistryInvariants(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range All() {
		if seen[k.Name] {
			t.Errorf("%s: duplicate kind", k.Name)
		}
		seen[k.Name] = true

		if k.Label == "" {
			t.Errorf("%s: no label", k.Name)
		}
		if k.Weight <= 0 || k.Weight > 1 {
			t.Errorf("%s: weight %v outside (0, 1]", k.Name, k.Weight)
		}
		if k.TTL <= 0 || k.ConfirmExtension <= 0 {
			t.Errorf("%s: TTL and ConfirmExtension must be positive", k.Name)
		}
		// A confirmation must be able to extend a fresh hazard at least a
		// little, or confirming would do nothing.
		if k.MaxLife <= k.TTL {
			t.Errorf("%s: MaxLife %v must exceed TTL %v", k.Name, k.MaxLife, k.TTL)
		}
		if k.MergeRadiusM != areaMergeM && k.MergeRadiusM != placeMergeM {
			t.Errorf("%s: merge radius %v is neither area nor place", k.Name, k.MergeRadiusM)
		}
	}
}

// TestAgreedLifetimes pins the lifetimes the design settled on, so changing
// one is a deliberate act rather than a side effect.
func TestAgreedLifetimes(t *testing.T) {
	cases := []struct {
		kind string
		ttl  time.Duration
	}{
		{"accident", 3 * time.Hour},
		{"pothole", 7 * 24 * time.Hour},
		{"construction", 24 * time.Hour},
		{"road_closed", 24 * time.Hour},
	}
	for _, tc := range cases {
		k, ok := Lookup(tc.kind)
		if !ok {
			t.Fatalf("kind %q is missing", tc.kind)
		}
		if k.TTL != tc.ttl {
			t.Errorf("%s TTL = %v, want %v", tc.kind, k.TTL, tc.ttl)
		}
	}
}

func TestMergeRadii(t *testing.T) {
	for name, want := range map[string]float64{
		"accident": 50, "road_closed": 50, "flooding": 50, "dangerous_junction": 50,
		"pothole": 25, "glass": 25, "slippery_surface": 25, "debris": 25,
	} {
		k, _ := Lookup(name)
		if k.MergeRadiusM != want {
			t.Errorf("%s merge radius = %v, want %v", name, k.MergeRadiusM, want)
		}
	}
}

func TestLookupUnknown(t *testing.T) {
	if _, ok := Lookup("meteor_strike"); ok {
		t.Error("an unknown kind was found")
	}
}

func TestAllReturnsACopy(t *testing.T) {
	a := All()
	a[0].Name = "tampered"
	if All()[0].Name == "tampered" {
		t.Error("All exposed the registry's backing array")
	}
}

func TestSubmissionValidateRejectsUnknownKind(t *testing.T) {
	s := Submission{Location: validPoint(), Kind: "meteor_strike"}
	if err := s.Validate(); err == nil {
		t.Fatal("expected an error for an unknown kind")
	}
	s.Kind = "glass"
	if err := s.Validate(); err != nil {
		t.Errorf("a registered kind was rejected: %v", err)
	}
}

// TestMigrationMatchesRegistry fails when a kind is added to the registry
// without a migration widening the CHECK constraint, or the reverse. Either
// mistake only shows up in production as a rejected insert.
func TestMigrationMatchesRegistry(t *testing.T) {
	files, err := filepath.Glob("../../db/migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found: %v", err)
	}
	sort.Strings(files)

	// The latest migration that (re)creates the constraint wins.
	var list string
	re := regexp.MustCompile(`(?s)ADD CONSTRAINT hazard_report_kind_check\s+CHECK \(kind IN \((.*?)\)\)`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(b), "-- +goose Down")
		if m := re.FindStringSubmatch(up); m != nil {
			list = m[1]
		}
	}
	if list == "" {
		t.Fatal("no migration defines hazard_report_kind_check")
	}

	var inDB []string
	for _, part := range strings.Split(list, ",") {
		inDB = append(inDB, strings.Trim(strings.TrimSpace(part), "'"))
	}
	sort.Strings(inDB)

	inGo := Names()
	if strings.Join(inDB, ",") != strings.Join(inGo, ",") {
		t.Errorf("kind lists differ\n  database: %v\n  registry: %v", inDB, inGo)
	}
}

func validPoint() geo.LatLon { return geo.LatLon{Lat: 30.27, Lon: -97.74} }
