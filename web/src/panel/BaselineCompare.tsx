import type { PlanResponse } from '../api/types';
import { distance } from '../lib/format';
import type { Units } from '../lib/format';

interface Props {
  plan: PlanResponse;
  showGhost: boolean;
  onToggleGhost: (on: boolean) => void;
  units: Units;
}

/**
 * What safety bought, and what detour it charged.
 *
 * Every response carries the distance-only baseline, so the trade is always
 * stateable: this much further, this much safer. Without it a safety score is
 * just a number the app asserts about itself.
 */
export function BaselineCompare({ plan, showGhost, onToggleGhost, units }: Props) {
  if (!plan.baseline_distance_m) return null;

  const extra = plan.distance_m - plan.baseline_distance_m;
  const safer = plan.safety_score - plan.baseline_safety_score;
  const isBaseline = plan.profile.name === 'shortest';

  return (
    <section className="compare">
      <h3>Versus the shortest way</h3>
      {isBaseline ? (
        <p>This is the shortest route. It is the thing every other profile is measured against.</p>
      ) : (
        <p className="compare__trade">
          <strong>{distance(Math.max(0, extra), units)} further</strong> than the direct route
          {safer > 0.5 ? (
            <>
              {' '}
              for <strong>{Math.round(safer)} points</strong> of safety.
            </>
          ) : (
            <> — and barely safer, so the direct route may serve you just as well.</>
          )}
        </p>
      )}
      <dl className="facts">
        <dt>Detour</dt>
        <dd>{plan.detour_ratio.toFixed(2)}×</dd>
        <dt>Shortest distance</dt>
        <dd>{distance(plan.baseline_distance_m, units)}</dd>
        <dt>Shortest safety score</dt>
        <dd>{Math.round(plan.baseline_safety_score)} / 100</dd>
      </dl>
      {!isBaseline && (
        <label className="toggle">
          <input
            type="checkbox"
            checked={showGhost}
            onChange={(e) => onToggleGhost(e.target.checked)}
          />
          <span>Show the shortest route on the map</span>
        </label>
      )}
    </section>
  );
}
