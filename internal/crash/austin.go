// Package crash ingests historical collision records and turns them into a
// per-edge crash pressure signal.
//
// Crash data is the empirical counterweight to the LTS rubric. LTS scores the
// road as designed; crash history catches the specific junction that is worse
// than its geometry suggests. Neither is sufficient alone — LTS is blind to
// local hazards, and crash counts are blind to exposure, since a road nobody
// dares ride records no crashes at all.
package crash

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SocrataEndpoint is Austin's published crash dataset, sourced from the TxDOT
// CRIS database and maintained by the city's Vision Zero programme.
const SocrataEndpoint = "https://data.austintexas.gov/resource/y2wy-tgr5.json"

// bicycleFilter selects crashes involving a cyclist.
//
// The dataset labels vehicle types in `units_involved` as a joined string,
// e.g. "Bicycle & Passenger car". Filtering on the injury-count columns would
// be wrong: bicycle_serious_injury_count only flags SERIOUS injuries, so
// every minor-injury bike crash would be silently dropped — about 90% of them.
const bicycleFilter = "units_involved like '%Bicycle%' AND latitude IS NOT NULL"

// Record is one crash as we store it.
type Record struct {
	SourceID string
	Lat, Lon float64
	Occurred time.Time
	Severity int // KABCO: 4 fatal, 3 serious, 2 minor, 1 possible, 0 none
	Raw      json.RawMessage
}

// socrataRow mirrors the fields we read from the API.
//
// Socrata returns most numbers as JSON strings, so the numeric fields use a
// tolerant type that accepts either form.
type socrataRow struct {
	CrisCrashID string    `json:"cris_crash_id"`
	CaseID      string    `json:"case_id"`
	Latitude    flexFloat `json:"latitude"`
	Longitude   flexFloat `json:"longitude"`
	Timestamp   string    `json:"crash_timestamp"`

	// Injury counts, used to derive severity. These are unambiguous, unlike
	// the crash_sev_id code whose meaning varies between CRIS versions.
	DeathCnt        flexInt `json:"death_cnt"`
	SusSeriousInjry flexInt `json:"sus_serious_injry_cnt"`
	NonIncapInjry   flexInt `json:"nonincap_injry_cnt"`
	PossInjry       flexInt `json:"poss_injry_cnt"`
}

// Fetch downloads cyclist-involved crashes from the last `years` years.
func Fetch(ctx context.Context, years int, logger *slog.Logger) ([]Record, error) {
	cutoff := time.Now().AddDate(-years, 0, 0).Format("2006-01-02T15:04:05")

	q := url.Values{}
	q.Set("$where", bicycleFilter+" AND crash_timestamp > '"+cutoff+"'")
	q.Set("$order", "crash_timestamp")
	q.Set("$limit", "50000")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, SocrataEndpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("crash: building request: %w", err)
	}

	client := &http.Client{Timeout: 3 * time.Minute}
	logger.Info("fetching crash records", "since", cutoff, "source", "austin_cris")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("crash: fetching: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("crash: source returned %s", resp.Status)
	}

	var raws []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raws); err != nil {
		return nil, fmt.Errorf("crash: decoding response: %w", err)
	}

	records := make([]Record, 0, len(raws))
	var skipped int
	for _, raw := range raws {
		rec, ok := parseRow(raw)
		if !ok {
			skipped++
			continue
		}
		records = append(records, rec)
	}

	logger.Info("fetched crash records",
		"usable", len(records), "skipped", skipped, "returned", len(raws))
	return records, nil
}

func parseRow(raw json.RawMessage) (Record, bool) {
	var row socrataRow
	if err := json.Unmarshal(raw, &row); err != nil {
		return Record{}, false
	}

	// A crash with no usable position cannot be attributed to a road.
	lat, lon := float64(row.Latitude), float64(row.Longitude)
	if lat == 0 || lon == 0 || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return Record{}, false
	}

	ts, err := time.Parse("2006-01-02T15:04:05.000", row.Timestamp)
	if err != nil {
		if ts, err = time.Parse(time.RFC3339, row.Timestamp); err != nil {
			return Record{}, false
		}
	}

	id := strings.TrimSpace(row.CrisCrashID)
	if id == "" {
		id = strings.TrimSpace(row.CaseID)
	}
	if id == "" {
		return Record{}, false
	}

	return Record{
		SourceID: id,
		Lat:      lat,
		Lon:      lon,
		Occurred: ts,
		Severity: severityFrom(row),
		Raw:      raw,
	}, true
}

// severityFrom derives a KABCO level from the injury counts.
//
// Reading the counts directly rather than trusting a severity code means the
// mapping is explicit and cannot drift when the upstream schema changes.
func severityFrom(r socrataRow) int {
	switch {
	case r.DeathCnt > 0:
		return 4 // K — fatal
	case r.SusSeriousInjry > 0:
		return 3 // A — suspected serious
	case r.NonIncapInjry > 0:
		return 2 // B — non-incapacitating
	case r.PossInjry > 0:
		return 1 // C — possible
	default:
		return 0 // O — no injury reported
	}
}

// --- tolerant JSON number types ------------------------------------------

// flexFloat accepts a JSON number or a quoted number, because Socrata emits
// both depending on the column.
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0
		return nil // a malformed number is treated as absent, not fatal
	}
	*f = flexFloat(v)
	return nil
}

type flexInt int

func (i *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*i = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*i = 0
		return nil
	}
	*i = flexInt(int(v))
	return nil
}
