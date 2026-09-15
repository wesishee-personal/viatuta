import type { SnapInfo } from '../api/types';
import { distance } from '../lib/format';
import type { Units } from '../lib/format';

/** Below this, the pin effectively landed on the road and saying so is noise. */
const WORTH_MENTIONING_M = 25;

/** The backend writes these in plain rider-facing English already. */
export function Warnings({ warnings }: { warnings?: string[] }) {
  if (!warnings?.length) return null;
  return (
    <section className="notice notice--warn">
      <ul>
        {warnings.map((w) => (
          <li key={w}>{w}</li>
        ))}
      </ul>
    </section>
  );
}

/**
 * Where the endpoints actually attached to the road network.
 *
 * Only shown when the pin moved far enough that a rider would notice — a
 * three-metre snap is correct behaviour, not something to report.
 */
export function SnapNotice({ snapped, units }: { snapped: SnapInfo[]; units: Units }) {
  const moved = snapped
    .map((s, i) => ({ ...s, which: i === 0 ? 'start' : i === snapped.length - 1 ? 'destination' : `stop ${i}` }))
    .filter((s) => s.distance_m >= WORTH_MENTIONING_M);

  if (moved.length === 0) return null;

  return (
    <section className="notice">
      <ul>
        {moved.map((s) => (
          <li key={s.which}>
            Moved your {s.which} {distance(s.distance_m, units)} to the nearest rideable road
            {s.road_name ? ` — ${s.road_name}` : ''}.
          </li>
        ))}
      </ul>
    </section>
  );
}
