package graph

import "testing"

// TestIndexOfDBID guards the translation between database ids and graph
// positions.
//
// These are different numbers because edges are re-sorted by source node when
// the graph loads. Conflating them is silent: the ids are in the same range,
// so the wrong lookup lands on a real edge rather than crashing, and data is
// simply attributed to the wrong road.
func TestIndexOfDBID(t *testing.T) {
	// Graph positions 0,1,2 holding database ids 7,3,5 — deliberately not
	// in order, as the sort by source node produces.
	g := &Graph{DBID: []int64{7, 3, 5}}
	if err := g.buildDBIndex(); err != nil {
		t.Fatalf("buildDBIndex: %v", err)
	}

	cases := []struct {
		dbID      int64
		wantIndex int32
		wantOK    bool
	}{
		{7, 0, true},
		{3, 1, true},
		{5, 2, true},
		{0, 0, false}, // a real id range, but no edge carries it
		{4, 0, false},
		{99, 0, false}, // beyond the table
		{-1, 0, false},
	}
	for _, tc := range cases {
		got, ok := g.IndexOfDBID(tc.dbID)
		if ok != tc.wantOK {
			t.Errorf("IndexOfDBID(%d) ok = %v, want %v", tc.dbID, ok, tc.wantOK)
			continue
		}
		if ok && got != tc.wantIndex {
			t.Errorf("IndexOfDBID(%d) = %d, want %d", tc.dbID, got, tc.wantIndex)
		}
	}
}

// TestIndexOfDBIDRoundTrips checks the property that actually matters: for
// every edge, translating its own database id must return it.
func TestIndexOfDBIDRoundTrips(t *testing.T) {
	g := &Graph{DBID: []int64{4, 0, 9, 2, 7, 1, 8, 3, 5, 6}}
	if err := g.buildDBIndex(); err != nil {
		t.Fatalf("buildDBIndex: %v", err)
	}
	for i, id := range g.DBID {
		got, ok := g.IndexOfDBID(id)
		if !ok || int(got) != i {
			t.Errorf("edge %d (db id %d) translated to %d (ok=%v)", i, id, got, ok)
		}
	}
}

// TestSparseDBIDsAreRejected checks the guard on the flat-array assumption.
// A sparse id space would otherwise allocate an enormous lookup table.
func TestSparseDBIDsAreRejected(t *testing.T) {
	g := &Graph{DBID: []int64{0, 1, 1 << 40}}
	if err := g.buildDBIndex(); err == nil {
		t.Error("expected sparse database ids to be rejected")
	}
}
