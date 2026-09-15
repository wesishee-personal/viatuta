import { useCallback, useEffect, useRef, useState } from 'react';
import { listHazards } from '../api/client';
import type { HazardReport } from '../api/types';

const DEBOUNCE_MS = 400;

/** [minLon, minLat, maxLon, maxLat] — the order GET /v1/hazards expects. */
export type BBox = [number, number, number, number];

export function formatBBox(b: BBox): string {
  return b.map((n) => n.toFixed(5)).join(',');
}

/** Grow a viewport bbox so small pans reuse the last fetch instead of
 *  re-querying on every frame the map settles. */
function pad(b: BBox, factor = 0.25): BBox {
  const dx = (b[2] - b[0]) * factor;
  const dy = (b[3] - b[1]) * factor;
  return [b[0] - dx, b[1] - dy, b[2] + dx, b[3] + dy];
}

function contains(outer: BBox, inner: BBox): boolean {
  return (
    outer[0] <= inner[0] && outer[1] <= inner[1] && outer[2] >= inner[2] && outer[3] >= inner[3]
  );
}

/**
 * Keeps the visible hazards in sync with the map viewport.
 *
 * The bbox is required by the API, so this hook is the only thing that knows
 * how to turn map bounds into that parameter.
 */
export function useHazards(viewport: BBox | null) {
  const [hazards, setHazards] = useState<HazardReport[]>([]);
  const fetched = useRef<BBox | null>(null);
  const inFlight = useRef<AbortController | null>(null);

  const load = useCallback(async (box: BBox, force: boolean) => {
    if (!force && fetched.current && contains(fetched.current, box)) return;

    const padded = pad(box);
    inFlight.current?.abort();
    const ctrl = new AbortController();
    inFlight.current = ctrl;

    try {
      const res = await listHazards(formatBBox(padded), ctrl.signal);
      if (ctrl.signal.aborted) return;
      fetched.current = padded;
      setHazards(res.hazards ?? []);
    } catch {
      // Hazards are an overlay. Failing to load them is not worth an error
      // state that would sit on top of a perfectly good route.
    }
  }, []);

  useEffect(() => {
    if (!viewport) return;
    const t = setTimeout(() => void load(viewport, false), DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [viewport, load]);

  useEffect(() => () => inFlight.current?.abort(), []);

  /** Called after a report or a confirmation: the backend rebuilds its
   *  routing overlay synchronously, so the map should agree immediately. */
  const refresh = useCallback(() => {
    if (viewport) void load(viewport, true);
  }, [viewport, load]);

  return { hazards, refresh };
}
