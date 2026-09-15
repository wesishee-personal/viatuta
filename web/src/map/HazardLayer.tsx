import { useMemo } from 'react';
import { Layer, Source } from 'react-map-gl/mapbox';
import type { HazardReport } from '../api/types';
import { HAZARD_SEVERITY } from '../api/types';
import type { HazardKind } from '../api/types';
import { HAZARD_COLOR } from './constants';
import { HAZARD_LAYER_ID, useLabelLayerId } from './MapView';

function severity(kind: string): number {
  return HAZARD_SEVERITY[kind as HazardKind] ?? 0.3;
}

/**
 * Rider-reported hazards, as circles over the map.
 *
 * Size carries corroboration and opacity carries severity, so a hazard three
 * people have confirmed reads as more real than one someone dropped in
 * passing — which is exactly how the routing penalty treats it.
 */
export function HazardLayer({ hazards }: { hazards: HazardReport[] }) {
  const beforeId = useLabelLayerId();

  const data = useMemo<GeoJSON.FeatureCollection>(
    () => ({
      type: 'FeatureCollection',
      features: hazards.map((h) => ({
        type: 'Feature',
        geometry: { type: 'Point', coordinates: [h.location.lon, h.location.lat] },
        properties: {
          id: h.id,
          kind: h.kind,
          severity: severity(h.kind),
          confirmations: h.confirmations,
        },
      })),
    }),
    [hazards],
  );

  return (
    <Source id="hazards" type="geojson" data={data}>
      <Layer
        id={HAZARD_LAYER_ID}
        type="circle"
        beforeId={beforeId}
        paint={{
          'circle-color': HAZARD_COLOR,
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
