import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import Map, { GeolocateControl, NavigationControl, ScaleControl } from 'react-map-gl/mapbox';
import type { MapMouseEvent, MapRef } from 'react-map-gl/mapbox';
import 'mapbox-gl/dist/mapbox-gl.css';

import type { GeoJSONGeometry, LatLon } from '../api/types';
import type { BBox } from '../state/useHazards';
import { AUSTIN_BOUNDS, INITIAL_VIEW, MAP_STYLE, MAPBOX_TOKEN, TOKEN_STATE } from './constants';

export const HAZARD_LAYER_ID = 'hazard-points';

/**
 * The id route and hazard layers insert themselves beneath, so street labels
 * stay legible on top. Resolved from the loaded style rather than hardcoded,
 * because addLayer throws on a beforeId that does not exist.
 */
const LabelLayerContext = createContext<string | undefined>(undefined);
export const useLabelLayerId = () => useContext(LabelLayerContext);

interface Props {
  /** A click on open map — drops a pin, or places a hazard in report mode. */
  onPick: (point: LatLon) => void;
  onHazardClick: (id: number) => void;
  onViewportChange: (bbox: BBox) => void;
  /** Browser geolocation, wired to the origin waypoint. */
  onLocate: (point: LatLon) => void;
  /** Fit the map to this geometry when it changes. */
  fitTo: GeoJSONGeometry | null;
  reporting: boolean;
  children: ReactNode;
}

export function MapView({
  onPick,
  onHazardClick,
  onViewportChange,
  onLocate,
  fitTo,
  reporting,
  children,
}: Props) {
  const mapRef = useRef<MapRef | null>(null);
  const [labelLayerId, setLabelLayerId] = useState<string | undefined>(undefined);
  const [loaded, setLoaded] = useState(false);

  const reportBounds = useCallback(() => {
    const map = mapRef.current;
    if (!map) return;
    const b = map.getBounds();
    if (!b) return;
    onViewportChange([b.getWest(), b.getSouth(), b.getEast(), b.getNorth()]);
  }, [onViewportChange]);

  const handleLoad = useCallback(() => {
    const map = mapRef.current?.getMap();
    // The first symbol layer is where labels begin; anything inserted before
    // it draws underneath every label in the style.
    const firstSymbol = map?.getStyle()?.layers?.find((l) => l.type === 'symbol');
    setLabelLayerId(firstSymbol?.id);
    setLoaded(true);
    reportBounds();
  }, [reportBounds]);

  const handleClick = useCallback(
    (e: MapMouseEvent) => {
      const hazard = e.features?.find((f) => f.layer?.id === HAZARD_LAYER_ID);
      if (hazard && !reporting) {
        const id = hazard.properties?.['id'];
        if (typeof id === 'number') onHazardClick(id);
        return;
      }
      onPick({ lat: e.lngLat.lat, lon: e.lngLat.lng });
    },
    [onPick, onHazardClick, reporting],
  );

  // Frame the whole route whenever a new one arrives, padded so neither
  // endpoint hides under the panel or the map controls.
  useEffect(() => {
    const map = mapRef.current;
    if (!map || !fitTo || fitTo.coordinates.length < 2) return;

    let [west, south, east, north] = [180, 90, -180, -90];
    for (const [lon, lat] of fitTo.coordinates) {
      west = Math.min(west, lon);
      east = Math.max(east, lon);
      south = Math.min(south, lat);
      north = Math.max(north, lat);
    }
    map.fitBounds(
      [
        [west, south],
        [east, north],
      ],
      { padding: 72, duration: 700, maxZoom: 16 },
    );
  }, [fitTo]);

  if (!MAPBOX_TOKEN) return <TokenProblem />;

  return (
    <Map
      ref={mapRef}
      mapboxAccessToken={MAPBOX_TOKEN}
      initialViewState={INITIAL_VIEW}
      mapStyle={MAP_STYLE}
      maxBounds={AUSTIN_BOUNDS}
      minZoom={9}
      interactiveLayerIds={[HAZARD_LAYER_ID]}
      cursor={reporting ? 'crosshair' : 'pointer'}
      onLoad={handleLoad}
      onMoveEnd={reportBounds}
      onClick={handleClick}
      style={{ width: '100%', height: '100%' }}
      reuseMaps
    >
      <NavigationControl position="top-right" showCompass={false} />
      <GeolocateControl
        position="top-right"
        trackUserLocation
        showUserHeading
        positionOptions={{ enableHighAccuracy: true }}
        onGeolocate={(e) => onLocate({ lat: e.coords.latitude, lon: e.coords.longitude })}
      />
      <ScaleControl position="bottom-right" />
      {loaded && (
        <LabelLayerContext.Provider value={labelLayerId}>{children}</LabelLayerContext.Provider>
      )}
    </Map>
  );
}

function TokenProblem() {
  if (TOKEN_STATE === 'secret') {
    return (
      <div className="map-fallback map-fallback--alarm">
        <h2>That is a secret token</h2>
        <p>
          <code>VITE_MAPBOX_TOKEN</code> starts with <code>sk.</code>. Secret tokens carry
          account-level scopes and this value would be compiled into the public JavaScript
          bundle, where anyone can read it.
        </p>
        <p>
          <strong>Revoke it in the Mapbox dashboard now</strong>, then put a public{' '}
          <code>pk.</code> token here instead.
        </p>
      </div>
    );
  }

  if (TOKEN_STATE === 'malformed') {
    return (
      <div className="map-fallback">
        <h2>Token doesn't look right</h2>
        <p>
          <code>VITE_MAPBOX_TOKEN</code> should start with <code>pk.</code> — check for a stray
          quote or a truncated paste.
        </p>
      </div>
    );
  }

  return (
    <div className="map-fallback">
      <h2>No Mapbox token</h2>
      <p>
        Copy <code>web/.env.example</code> to <code>web/.env</code> and set{' '}
        <code>VITE_MAPBOX_TOKEN</code> to a public <code>pk.</code> token, then restart the dev
        server.
      </p>
    </div>
  );
}
