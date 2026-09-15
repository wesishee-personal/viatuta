import { useCallback, useEffect, useMemo, useState } from 'react';
import type { LatLon } from './api/types';
import { HazardPopup } from './hazards/HazardPopup';
import { ReportHazardForm } from './hazards/ReportHazardForm';
import type { Units } from './lib/format';
import { HazardLayer } from './map/HazardLayer';
import { MapView } from './map/MapView';
import { BaselineLayer, RouteLayer } from './map/RouteLayer';
import { WaypointMarkers } from './map/WaypointMarkers';
import { PlanPanel } from './panel/PlanPanel';
import type { Selection } from './state/selection';
import type { BBox } from './state/useHazards';
import { useHazards } from './state/useHazards';
import { usePlan } from './state/usePlan';

export default function App() {
  const [origin, setOrigin] = useState<LatLon | null>(null);
  const [destination, setDestination] = useState<LatLon | null>(null);
  const [units, setUnits] = useState<Units>('imperial');
  const [showGhost, setShowGhost] = useState(false);
  const [viewport, setViewport] = useState<BBox | null>(null);
  const [selection, setSelection] = useState<Selection | null>(null);

  const [reporting, setReporting] = useState(false);
  const [pendingHazard, setPendingHazard] = useState<LatLon | null>(null);
  const [selectedHazard, setSelectedHazard] = useState<number | null>(null);

  const { settings, setSettings, retryWith, status, plan, baseline, error } = usePlan(
    origin,
    destination,
    showGhost,
  );
  const { hazards, refresh } = useHazards(viewport);

  const selected = useMemo(
    () => hazards.find((h) => h.id === selectedHazard) ?? null,
    [hazards, selectedHazard],
  );

  const handlePick = useCallback(
    (point: LatLon) => {
      if (reporting) {
        setPendingHazard(point);
        setReporting(false);
        return;
      }
      setSelectedHazard(null);
      // First click sets the start, second the destination, and after that
      // each click moves the destination — which is what someone comparing a
      // few nearby places actually wants.
      setSelection(null);
      if (!origin) setOrigin(point);
      else setDestination(point);
    },
    [origin, reporting],
  );

  const handleMove = useCallback((which: 'origin' | 'destination', point: LatLon) => {
    setSelection(null);
    if (which === 'origin') setOrigin(point);
    else setDestination(point);
  }, []);

  // A selection names a slice of one particular route. Once the route is
  // being recomputed it refers to nothing, so drop it rather than leave the
  // legend pointing at segments that no longer exist.
  useEffect(() => {
    if (status !== 'ready') setSelection(null);
  }, [status]);

  // Changing the profile changes the route even though the pins have not
  // moved, so the same reasoning applies.
  useEffect(() => {
    setSelection(null);
  }, [settings]);

  const clear = useCallback(() => {
    setOrigin(null);
    setDestination(null);
    setSelectedHazard(null);
    setSelection(null);
  }, []);

  const swap = useCallback(() => {
    setOrigin(destination);
    setDestination(origin);
  }, [origin, destination]);

  // Only frame the map on a finished route, never mid-load — refitting to a
  // stale geometry while a new one is in flight is disorienting.
  const fitTo = status === 'ready' ? (plan?.geometry ?? null) : null;

  return (
    <div className="app">
      <PlanPanel
        origin={origin}
        destination={destination}
        status={status}
        plan={plan}
        error={error}
        settings={settings}
        onSettings={setSettings}
        onRetryWith={retryWith}
        units={units}
        onUnits={setUnits}
        showGhost={showGhost}
        onToggleGhost={setShowGhost}
        reporting={reporting}
        onToggleReporting={() => {
          setReporting((v) => !v);
          setPendingHazard(null);
        }}
        selection={selection}
        onSelect={setSelection}
        onClear={clear}
        onSwap={swap}
      />

      <main className="map">
        <MapView
          onPick={handlePick}
          onHazardClick={setSelectedHazard}
          onViewportChange={setViewport}
          onLocate={setOrigin}
          fitTo={fitTo}
          reporting={reporting}
        >
          {showGhost && <BaselineLayer geometry={baseline?.geometry ?? null} />}
          <RouteLayer
            geometry={status === 'ready' ? (plan?.geometry ?? null) : null}
            segments={plan?.segments}
            selection={selection}
          />
          <HazardLayer hazards={hazards} />
          <WaypointMarkers origin={origin} destination={destination} onMove={handleMove} />
          {selected && (
            <HazardPopup
              hazard={selected}
              onClose={() => setSelectedHazard(null)}
              onConfirmed={refresh}
            />
          )}
          {pendingHazard && (
            <ReportHazardForm
              location={pendingHazard}
              onCancel={() => setPendingHazard(null)}
              onReported={() => {
                setPendingHazard(null);
                refresh();
              }}
            />
          )}
        </MapView>
        {reporting && (
          <div className="map__banner">Click the map where the hazard is</div>
        )}
      </main>
    </div>
  );
}
