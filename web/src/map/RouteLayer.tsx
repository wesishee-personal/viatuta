import { useMemo } from 'react';
import { Layer, Source } from 'react-map-gl/mapbox';
import type { GeoJSONGeometry, RouteSegment } from '../api/types';
import type { Selection } from '../state/selection';
import { BASELINE_COLOR, LTS_COLORS, ROUTE_CASING, ROUTE_COLOR } from './constants';
import { useLabelLayerId } from './MapView';

/** The API returns a bare geometry, not a Feature, so wrap it before handing
 *  it to a GeoJSON source. */
function asFeature(geometry: GeoJSONGeometry): GeoJSON.Feature {
  return { type: 'Feature', geometry, properties: {} };
}

/**
 * One Feature per segment, sliced out of the route's coordinates.
 *
 * `end` is inclusive because consecutive segments share a junction vertex, so
 * the slice runs to `end + 1`. Getting that wrong leaves a one-pixel gap at
 * every corner, which reads as a dashed line at low zoom.
 */
function asSegments(geometry: GeoJSONGeometry, segments: RouteSegment[]): GeoJSON.FeatureCollection {
  return {
    type: 'FeatureCollection',
    features: segments.map((s, i) => ({
      type: 'Feature',
      id: i,
      geometry: {
        type: 'LineString',
        coordinates: geometry.coordinates.slice(s.start, s.end + 1),
      },
      properties: { lts: s.lts, infra: s.infra, length_m: s.length_m },
    })),
  };
}

// Colour by traffic stress, using the same scale as the breakdown bars so the
// line and the legend agree by construction rather than by coincidence.
const LTS_COLOR_EXPR = [
  'match',
  ['get', 'lts'],
  1, LTS_COLORS['lts1'],
  2, LTS_COLORS['lts2'],
  3, LTS_COLORS['lts3'],
  4, LTS_COLORS['lts4'],
  LTS_COLORS['unknown'],
];

/**
 * A zoom-interpolated width that thickens whichever segments are selected.
 *
 * Note the nesting: `interpolate` must be OUTSIDE, with the per-stop values
 * varying. Mapbox rejects a `zoom` expression nested inside `case` — it may
 * only be the direct input of a top-level `step` or `interpolate` — so the
 * obvious `['case', hit, WIDE, BASE]` is silently invalid at runtime.
 */
function widthExpr(hit: unknown, near: number, far: number, nearSel: number, farSel: number) {
  const at = (plain: number, selected: number) => (hit ? ['case', hit, selected, plain] : plain);
  return ['interpolate', ['linear'], ['zoom'], 10, at(near, nearSel), 16, at(far, farSel)];
}

/** Faded, but still present — the rest of the route is context, not noise. */
const DIMMED = 0.22;

interface Props {
  geometry: GeoJSONGeometry | null;
  segments?: RouteSegment[];
  selection: Selection | null;
}

export function RouteLayer({ geometry, segments, selection }: Props) {
  const beforeId = useLabelLayerId();

  const data = useMemo(() => {
    if (!geometry) return null;
    return segments?.length ? asSegments(geometry, segments) : asFeature(geometry);
  }, [geometry, segments]);

  // A server without per-segment support still gets a route, just a plain one.
  const plain = !segments?.length;

  const { opacity, lineWidth, casingWidth } = useMemo(() => {
    const hit = selection ? ['==', ['get', selection.kind], selection.value] : null;
    return {
      opacity: hit ? ['case', hit, 1, DIMMED] : 1,
      lineWidth: widthExpr(hit, 3, 7, 6, 12),
      casingWidth: widthExpr(hit, 5, 11, 8, 16),
    };
  }, [selection]);

  if (!data) return null;

  return (
    <Source id="route" type="geojson" data={data}>
      {/* The casing carries the same opacity as the line above it — without
          that, dimmed segments glow white from underneath. */}
      <Layer
        id="route-casing"
        type="line"
        beforeId={beforeId}
        layout={{ 'line-cap': 'round', 'line-join': 'round' }}
        paint={{
          'line-color': ROUTE_CASING,
          'line-width': casingWidth,
          'line-opacity': plain ? 0.9 : opacity,
        } as never}
      />
      <Layer
        id="route-line"
        type="line"
        beforeId={beforeId}
        layout={{ 'line-cap': 'round', 'line-join': 'round' }}
        paint={{
          'line-color': plain ? ROUTE_COLOR : LTS_COLOR_EXPR,
          'line-width': lineWidth,
          'line-opacity': plain ? 1 : opacity,
        } as never}
      />
    </Source>
  );
}

/**
 * The shortest-distance route, drawn as a ghost behind the safe one.
 *
 * This is what makes `detour_ratio` mean something: the rider sees the ride
 * the safety model declined to give them, not just a number describing it.
 */
export function BaselineLayer({ geometry }: { geometry: GeoJSONGeometry | null }) {
  const beforeId = useLabelLayerId();
  if (!geometry) return null;

  return (
    <Source id="baseline" type="geojson" data={asFeature(geometry)}>
      <Layer
        id="baseline-line"
        type="line"
        beforeId={beforeId}
        layout={{ 'line-cap': 'butt', 'line-join': 'round' }}
        paint={{
          'line-color': BASELINE_COLOR,
          'line-width': ['interpolate', ['linear'], ['zoom'], 10, 2, 16, 4],
          'line-dasharray': [2, 2],
          'line-opacity': 0.85,
        }}
      />
    </Source>
  );
}
