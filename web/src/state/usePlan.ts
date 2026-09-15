import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError, NetworkError, planRoute, readyz } from '../api/client';
import type { LatLon, PlanRequest, PlanResponse, Profile, ProfileName } from '../api/types';

/** Redundant keystrokes and marker drags are cheap; route searches are not. */
const DEBOUNCE_MS = 250;
const WARMUP_POLL_MS = 2_000;

export interface PlanSettings {
  profile: ProfileName;
  /** When set, replaces the named profile entirely. Always a complete object —
   *  the backend's Validate() rejects partial ones. */
  custom: Profile | null;
  night: boolean;
}

export const DEFAULT_SETTINGS: PlanSettings = {
  profile: 'comfortable',
  custom: null,
  night: false,
};

export type PlanStatus = 'idle' | 'loading' | 'ready' | 'error' | 'warming-up';

export interface PlanState {
  status: PlanStatus;
  plan: PlanResponse | null;
  /** The distance-only route, fetched separately because the API returns the
   *  baseline's numbers but not its geometry. Only populated while `compare`
   *  is on. */
  baseline: PlanResponse | null;
  error: ApiError | NetworkError | null;
}

/**
 * Build the request body literally.
 *
 * The API decodes with DisallowUnknownFields, so this must never be produced
 * by spreading component state — an extra key is a 400, not a no-op.
 */
function buildRequest(waypoints: LatLon[], settings: PlanSettings): PlanRequest {
  const req: PlanRequest = { waypoints };
  if (settings.custom) {
    req.custom = settings.custom;
  } else {
    req.profile = settings.profile;
  }
  if (settings.night) req.night = true;
  return req;
}

function isAbort(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError';
}

/**
 * Plans a route whenever the endpoints or the rider's settings change.
 *
 * Owns the profile settings rather than taking them as props, so that the
 * unroutable-recovery flow can re-plan with a different profile in one call.
 */
export function usePlan(origin: LatLon | null, destination: LatLon | null, compare: boolean) {
  const [settings, setSettings] = useState<PlanSettings>(DEFAULT_SETTINGS);
  const [state, setState] = useState<PlanState>({
    status: 'idle',
    plan: null,
    baseline: null,
    error: null,
  });

  // One controller per in-flight plan. A fast marker drag fires several
  // requests; only the last may paint, or the map briefly shows a route that
  // does not connect the markers the rider is looking at.
  const inFlight = useRef<AbortController | null>(null);

  const run = useCallback(
    async (o: LatLon, d: LatLon, s: PlanSettings, withBaseline: boolean) => {
      inFlight.current?.abort();
      const ctrl = new AbortController();
      inFlight.current = ctrl;

      setState((prev) => ({ ...prev, status: 'loading', error: null }));

      const waypoints = [o, d];
      // The baseline is a separate search and is allowed to fail on its own —
      // a missing comparison should never cost the rider their actual route.
      const mainReq = planRoute(buildRequest(waypoints, s), ctrl.signal);
      const baseReq =
        withBaseline && s.profile !== 'shortest' && !s.custom
          ? planRoute({ waypoints, profile: 'shortest' }, ctrl.signal).catch(() => null)
          : Promise.resolve(null);

      try {
        const [plan, baseline] = await Promise.all([mainReq, baseReq]);
        if (ctrl.signal.aborted) return;
        setState({ status: 'ready', plan, baseline, error: null });
      } catch (err) {
        if (ctrl.signal.aborted || isAbort(err)) return;
        const error = err instanceof ApiError || err instanceof NetworkError
          ? err
          : new NetworkError('something went wrong', err);
        setState({
          status: error instanceof ApiError && error.isWarmingUp ? 'warming-up' : 'error',
          plan: null,
          baseline: null,
          error,
        });
      }
    },
    [],
  );

  useEffect(() => {
    if (!origin || !destination) {
      inFlight.current?.abort();
      setState({ status: 'idle', plan: null, baseline: null, error: null });
      return;
    }
    const t = setTimeout(() => void run(origin, destination, settings, compare), DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [origin, destination, settings, compare, run]);

  // While the graph loads at startup every plan 503s. Poll readiness and
  // retry by itself rather than making the rider work out that waiting fixes
  // it.
  useEffect(() => {
    if (state.status !== 'warming-up' || !origin || !destination) return;
    const t = setTimeout(async () => {
      try {
        const health = await readyz();
        if (health.graph === 'ok') void run(origin, destination, settings, compare);
      } catch {
        /* still starting; the next tick tries again */
      }
    }, WARMUP_POLL_MS);
    return () => clearTimeout(t);
  }, [state.status, origin, destination, settings, compare, run]);

  useEffect(() => () => inFlight.current?.abort(), []);

  /** Re-plan with a different built-in profile — the escape hatch offered
   *  when the current profile has no route at all. */
  const retryWith = useCallback((profile: ProfileName) => {
    setSettings((prev) => ({ ...prev, profile, custom: null }));
  }, []);

  return { settings, setSettings, retryWith, ...state };
}
