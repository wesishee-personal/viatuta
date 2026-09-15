// Package geo holds the small geographic primitives shared by ingest,
// graph building, and routing.
//
// Keeping these in one place matters more than it looks: distance is
// computed in the innermost loop of the router, and having two subtly
// different haversine implementations is a classic source of bugs where
// routes are correct but costs are not.
package geo

import "math"

// EarthRadiusM is the mean radius of the Earth in meters.
const EarthRadiusM = 6371008.8

// LatLon is a WGS84 coordinate — the coordinate system GPS and OSM use,
// and what PostGIS calls SRID 4326.
//
// Note the field order: latitude first. GeoJSON and PostGIS use
// [longitude, latitude] instead, which is a constant source of confusion, so
// conversions to those formats are done by explicit helpers below rather
// than by anything that could silently swap them.
type LatLon struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Valid reports whether the coordinate is within the legal ranges.
func (p LatLon) Valid() bool {
	return p.Lat >= -90 && p.Lat <= 90 && p.Lon >= -180 && p.Lon <= 180
}

// GeoJSON returns the coordinate in GeoJSON's [lon, lat] order.
func (p LatLon) GeoJSON() [2]float64 { return [2]float64{p.Lon, p.Lat} }

// DistanceM returns the great-circle distance between two points in meters,
// using the haversine formula.
//
// Haversine treats the Earth as a sphere. It is off by up to ~0.5% versus a
// proper ellipsoidal calculation, which is irrelevant here: we use it for
// edge lengths of tens of meters and for the A* heuristic, where a slight
// *underestimate* is actually required for correctness.
func DistanceM(a, b LatLon) float64 {
	lat1 := a.Lat * math.Pi / 180
	lat2 := b.Lat * math.Pi / 180
	dLat := lat2 - lat1
	dLon := (b.Lon - a.Lon) * math.Pi / 180

	s := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * EarthRadiusM * math.Asin(math.Sqrt(s))
}

// BearingDeg returns the initial compass bearing from a to b, in degrees
// clockwise from north (0 = north, 90 = east).
//
// The router uses this to classify turns: the signed difference between the
// bearing of the edge you arrived on and the one you are leaving on tells us
// whether this is a left turn across traffic or a gentle continuation.
func BearingDeg(a, b LatLon) float64 {
	lat1 := a.Lat * math.Pi / 180
	lat2 := b.Lat * math.Pi / 180
	dLon := (b.Lon - a.Lon) * math.Pi / 180

	y := math.Sin(dLon) * math.Cos(lat2)
	x := math.Cos(lat1)*math.Sin(lat2) - math.Sin(lat1)*math.Cos(lat2)*math.Cos(dLon)

	deg := math.Atan2(y, x) * 180 / math.Pi
	return math.Mod(deg+360, 360)
}

// TurnAngleDeg returns the change in heading when moving from bearing `from`
// to bearing `to`, normalized to [-180, 180).
//
// Negative is a left turn, positive is a right turn, and values near zero
// mean you continued straight. Left turns are the dangerous ones in
// right-hand traffic, which is why the sign is preserved rather than taking
// an absolute value.
//
// An exact reversal returns -180 rather than +180; a u-turn is neither a
// left nor a right, so callers should classify it by magnitude, not sign.
func TurnAngleDeg(from, to float64) float64 {
	d := math.Mod(to-from+540, 360) - 180
	return d
}
