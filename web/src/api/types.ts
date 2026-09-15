// Mirrors of the Go types the API exchanges. These are hand-maintained
// against the backend structs — the file:line references are load-bearing,
// so update both sides together.
//
// The single most important thing to know here: the API decodes request
// bodies with DisallowUnknownFields (internal/httpapi/errors.go:81). Any
// extra or misspelled field is a hard 400, so request bodies must be built
// literally rather than spread from component state.

/** internal/geo/geo.go:22 — note the API takes lat/lon objects, but returns
 *  GeoJSON [lon, lat] pairs in route geometry. Do not mix them up. */
export interface LatLon {
  lat: number;
  lon: number;
}

/** A GeoJSON coordinate: [longitude, latitude]. */
export type Position = [number, number];

// ---------------------------------------------------------------------------
// Rider profiles — internal/safety/profile.go:10
// ---------------------------------------------------------------------------

export const PROFILE_NAMES = ['cautious', 'comfortable', 'confident', 'shortest'] as const;
export type ProfileName = (typeof PROFILE_NAMES)[number];

export interface Profile {
  name: string;
  max_lts: number;
  stress_budget_m: number;
  weight_lts: number;
  weight_crash: number;
  weight_grade: number;
  weight_surface: number;
  weight_light: number;
  weight_turn: number;
  weight_hazard: number;
  max_detour_ratio: number;
  night: boolean;
}

/** The seven tunable weights, in the order the advanced panel shows them. */
export const WEIGHT_KEYS = [
  'weight_lts',
  'weight_crash',
  'weight_grade',
  'weight_surface',
  'weight_light',
  'weight_turn',
  'weight_hazard',
] as const;
export type WeightKey = (typeof WEIGHT_KEYS)[number];

// ---------------------------------------------------------------------------
// Route planning — internal/httpapi/route_handler.go:20
// ---------------------------------------------------------------------------

export interface PlanRequest {
  waypoints: LatLon[];
  profile?: string;
  /** Overrides `profile` entirely. Must be a COMPLETE Profile: the backend's
   *  Validate() (internal/safety/profile.go:102) rejects zero values for
   *  max_lts and max_detour_ratio, so a partial object always 400s. */
  custom?: Profile;
  night?: boolean;
}

export interface GeoJSONGeometry {
  type: 'LineString';
  coordinates: Position[];
}

export interface SnapInfo {
  requested: LatLon;
  distance_m: number;
  road_name?: string;
}

/** internal/safety/cost.go:170 */
export interface Breakdown {
  by_lts_m: Record<string, number>;
  by_infra_m: Record<string, number>;
  crash_exposure_m: number;
  climb_m: number;
  descent_m: number;
  steep_m: number;
  hazard_m: number;
}

/** A run of consecutive road segments sharing the same traffic stress and
 *  facility type, indexed into `geometry.coordinates`.
 *
 *  `end` is INCLUSIVE and equals the next segment's `start` — they share that
 *  junction vertex — so slice with `coordinates.slice(start, end + 1)` to get
 *  polylines that join up instead of ones with gaps at every corner. */
export interface RouteSegment {
  start: number;
  end: number;
  /** 1..4, or 0 for an edge the scorer has not classified. */
  lts: number;
  /** Same vocabulary as `breakdown.by_infra_m` keys. */
  infra: string;
  length_m: number;
}

export interface PlanResponse {
  geometry: GeoJSONGeometry;

  /** Optional: a server built before per-segment detail landed omits this,
   *  and the map falls back to drawing one undifferentiated line. */
  segments?: RouteSegment[];
  distance_m: number;
  duration_s: number;
  cost: number;
  safety_score: number;
  breakdown: Breakdown;
  stress_budget_m: number;
  stress_budget_used_m: number;
  baseline_distance_m: number;
  baseline_safety_score: number;
  detour_ratio: number;
  profile: Profile;
  warnings?: string[];
  snapped: SnapInfo[];
  edge_count: number;
  compute_ms: number;
}

// ---------------------------------------------------------------------------
// Hazards — internal/hazard/store.go:17, internal/hazard/hazard.go:47
// ---------------------------------------------------------------------------

/** internal/hazard/hazard.go:26. Ordered least to most severe, matching the
 *  weights the backend assigns each kind. */
export const HAZARD_KINDS = [
  'other',
  'debris',
  'pothole',
  'poor_visibility',
  'aggressive_traffic',
  'blocked_lane',
  'construction',
  'dangerous_junction',
] as const;
export type HazardKind = (typeof HAZARD_KINDS)[number];

export const HAZARD_LABELS: Record<HazardKind, string> = {
  other: 'Something else',
  debris: 'Debris or broken glass',
  pothole: 'Pothole or bad surface',
  poor_visibility: 'Poor visibility',
  aggressive_traffic: 'Aggressive traffic',
  blocked_lane: 'Blocked bike lane',
  construction: 'Construction',
  dangerous_junction: 'Dangerous junction',
};

/** Matches the backend's per-kind weights (internal/hazard/hazard.go:26), so
 *  the map legend and the routing penalty agree on what counts as serious. */
export const HAZARD_SEVERITY: Record<HazardKind, number> = {
  other: 0.3,
  debris: 0.4,
  pothole: 0.5,
  poor_visibility: 0.6,
  aggressive_traffic: 0.7,
  blocked_lane: 0.8,
  construction: 0.9,
  dangerous_junction: 1.0,
};

/** Enforced server-side at internal/hazard/store.go:31. */
export const HAZARD_NOTES_MAX = 500;

export interface HazardSubmission {
  location: LatLon;
  kind: HazardKind;
  notes?: string;
}

export interface HazardReport {
  id: number;
  location: LatLon;
  kind: string;
  notes?: string;
  confirmations: number;
  created_at: string;
  expires_at: string;
}

export interface HazardListResponse {
  hazards: HazardReport[];
  count: number;
}

// ---------------------------------------------------------------------------
// Errors and health
// ---------------------------------------------------------------------------

/** internal/httpapi/errors.go:23. Clients branch on `code`, never on wording. */
export type ErrorCode =
  | 'bad_request'
  | 'unauthorized'
  | 'forbidden'
  | 'not_found'
  | 'conflict'
  | 'internal_error'
  | 'unroutable';

export interface ErrorResponse {
  code: ErrorCode;
  message: string;
  details?: Record<string, string>;
}

/** internal/httpapi/server.go:100 */
export interface HealthResponse {
  status: 'ok' | 'degraded' | 'starting';
  uptime: string;
  database?: string;
  graph?: string;
  nodes?: number;
  edges?: number;
}
