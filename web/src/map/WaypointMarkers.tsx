import { Marker } from 'react-map-gl/mapbox';
import type { LatLon } from '../api/types';

interface Props {
  origin: LatLon | null;
  destination: LatLon | null;
  onMove: (which: 'origin' | 'destination', point: LatLon) => void;
}

/**
 * The two endpoints, draggable.
 *
 * Dragging re-plans on drop rather than continuously: a route search is not
 * cheap, and a line that thrashes while the pin moves is harder to read than
 * one that snaps into place once.
 */
export function WaypointMarkers({ origin, destination, onMove }: Props) {
  return (
    <>
      {origin && (
        <Marker
          longitude={origin.lon}
          latitude={origin.lat}
          anchor="bottom"
          draggable
          onDragEnd={(e) => onMove('origin', { lat: e.lngLat.lat, lon: e.lngLat.lng })}
        >
          <Pin label="A" kind="origin" />
        </Marker>
      )}
      {destination && (
        <Marker
          longitude={destination.lon}
          latitude={destination.lat}
          anchor="bottom"
          draggable
          onDragEnd={(e) => onMove('destination', { lat: e.lngLat.lat, lon: e.lngLat.lng })}
        >
          <Pin label="B" kind="destination" />
        </Marker>
      )}
    </>
  );
}

function Pin({ label, kind }: { label: string; kind: 'origin' | 'destination' }) {
  return (
    <div className={`pin pin--${kind}`} title={kind === 'origin' ? 'Start' : 'Destination'}>
      <span>{label}</span>
    </div>
  );
}
