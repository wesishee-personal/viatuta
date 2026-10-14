import { useMemo } from 'react';
import { Layer, Source } from 'react-map-gl/mapbox';
import type { HazardReport } from '../api/types';
import { useHazardKinds } from '../state/HazardKinds';
import { CLOSURE_COLOR, HAZARD_COLOR } from './constants';
import { HAZARD_LAYER_ID, useLabelLayerId } from './MapView';

/** Used until the kinds load, or for a kind this client has not heard of. */
const DEFAULT_SEVERITY = 0.3;

/**
 * Rider-reported hazards, as circles over the map.
 *
 * Size carries corroboration and opacity carries severity, so a hazard three
 * people have confirmed reads as more real than one someone dropped in
 * passing — which is exactly how the routing penalty treats it. Road closures
 * get their own colour, since they make a different claim: the road cannot be
 * ridden at all.
 */
export function HazardLayer({ hazards }: { hazards: HazardReport[] }) {
  const beforeId = useLabelLayerId();
  const { byKind } = useHazardKinds();

  const data = useMemo<GeoJSON.FeatureCollection>(
    () => ({
      type: 'FeatureCollection',
      features: hazards.map((h) => {
        const info = byKind(h.kind);
        return {
          type: 'Feature',
          geometry: { type: 'Point', coordinates: [h.location.lon, h.location.lat] },
          properties: {
            id: h.id,
            kind: h.kind,
            severity: info?.weight ?? DEFAULT_SEVERITY,
            closure: info?.closure ?? false,
            confirmations: h.confirmations,
          },
        };
      }),
    }),
    [hazards, byKind],
  );

  return (
    <Source id="hazards" type="geojson" data={data}>
      <Layer
        id={HAZARD_LAYER_ID}
        type="circle"
        beforeId={beforeId}
        paint={{
          'circle-color': ['case', ['get', 'closure'], CLOSURE_COLOR, HAZARD_COLOR],
          'circle-opacity': ['+', 0.45, ['*', 0.45, ['get', 'severity']]],
          'circle-stroke-color': '#ffffff',
          'circle-stroke-width': 1.5,
          'circle-radius': [
            'interpolate',
            ['linear'],
            ['zoom'],
            10,
            ['+', 3, ['min', 3, ['get', 'confirmations']]],
            16,
            ['+', 7, ['*', 2, ['min', 4, ['get', 'confirmations']]]],
          ],
        }}
      />
    </Source>
  );
}
