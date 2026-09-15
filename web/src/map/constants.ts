import type { LngLatBoundsLike } from 'mapbox-gl';

const RAW_TOKEN = import.meta.env.VITE_MAPBOX_TOKEN ?? '';

/**
 * What kind of token we were handed.
 *
 * A `pk.` token is *meant* to ship in the bundle — the browser has to send it
 * to Mapbox, so it cannot be hidden, and proxying tiles through our own server
 * to conceal it is prohibited by Mapbox's terms. The real protection is a URL
 * restriction on the token itself.
 *
 * An `sk.` token is the opposite: a secret with account-level scopes that must
 * never leave a server. Pasting one here is the one genuinely dangerous
 * mistake available, so we refuse to use it rather than quietly publishing it.
 */
export type TokenState = 'ok' | 'missing' | 'secret' | 'malformed';

export function classifyToken(token: string): TokenState {
  if (!token) return 'missing';
  if (token.startsWith('sk.')) return 'secret';
  if (!token.startsWith('pk.')) return 'malformed';
  return 'ok';
}

export const TOKEN_STATE = classifyToken(RAW_TOKEN);

/** Empty unless the token is a usable public one. */
export const MAPBOX_TOKEN = TOKEN_STATE === 'ok' ? RAW_TOKEN : '';

/**
 * A muted basemap. The route, the hazards, and the stress colours are the
 * only things on this page that should carry meaning through colour, so the
 * basemap deliberately stays out of their way. light-v11 also has stable
 * layer ids, which `ROUTE_BEFORE_ID` depends on.
 */
export const MAP_STYLE = 'mapbox://styles/mapbox/light-v11';

/**
 * Insert route lines beneath the street labels so road names stay readable
 * where the route covers them. This id exists in the Mapbox Standard and
 * light/dark styles; MapView falls back to appending on top if it is absent.
 */
export const ROUTE_BEFORE_ID = 'road-label';

/**
 * The region the routing graph actually covers.
 *
 * Derived from the cached terrain tiles in `data/elevation/` (zoom 13,
 * x 1865–1877, y 3359–3377), which were downloaded to cover exactly the
 * Austin OSM extract the graph is built from. Panning outside this is
 * pointless: every click out here snaps to nothing and returns 422.
 */
export const AUSTIN_BOUNDS: LngLatBoundsLike = [
  [-98.042, 30.069],
  [-97.471, 30.789],
];

/** Downtown Austin — a sensible place to open. */
export const INITIAL_VIEW = {
  longitude: -97.7431,
  latitude: 30.2672,
  zoom: 12.5,
} as const;

// ---------------------------------------------------------------------------
// Colour
// ---------------------------------------------------------------------------

/**
 * Level of Traffic Stress, 1 (a quiet street a child could ride) to 4 (an
 * arterial). Green to red, because this is the one scale on the page where
 * that convention means exactly what a rider expects it to.
 */
export const LTS_COLORS: Record<string, string> = {
  lts1: '#1a8a4a',
  lts2: '#8cb93c',
  lts3: '#e8a33d',
  lts4: '#d2402f',
  unknown: '#9aa0a6',
};

export const LTS_LABELS: Record<string, string> = {
  lts1: 'LTS 1 — relaxed',
  lts2: 'LTS 2 — most adults',
  lts3: 'LTS 3 — confident riders',
  lts4: 'LTS 4 — stressful',
  unknown: 'Unclassified',
};

/** Facility types, ordered best to worst — internal/safety/cost.go:189. */
export const INFRA_ORDER = [
  'protected_track',
  'path',
  'buffered_lane',
  'painted_lane',
  'shared_lane',
  'pedestrian',
  'none',
] as const;

export const INFRA_COLORS: Record<string, string> = {
  protected_track: '#1a8a4a',
  path: '#43a05c',
  buffered_lane: '#8cb93c',
  painted_lane: '#d4c33a',
  shared_lane: '#e8a33d',
  pedestrian: '#a88bc4',
  none: '#c4c9ce',
};

export const INFRA_LABELS: Record<string, string> = {
  protected_track: 'Protected track',
  path: 'Off-street path',
  buffered_lane: 'Buffered lane',
  painted_lane: 'Painted lane',
  shared_lane: 'Shared lane',
  pedestrian: 'Pedestrian way',
  none: 'No bike facility',
};

export const ROUTE_COLOR = '#2b6cb0';
export const ROUTE_CASING = '#ffffff';
export const BASELINE_COLOR = '#8a9099';
export const HAZARD_COLOR = '#c2410c';
