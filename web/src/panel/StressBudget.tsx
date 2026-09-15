import type { PlanResponse } from '../api/types';
import { distance } from '../lib/format';
import type { Units } from '../lib/format';

/**
 * Exposure above the rider's comfort threshold: what was allowed, and what
 * this route actually spends.
 *
 * This is the promise the whole safety model rests on — Austin's low-stress
 * network is islanded by arterials, so a hard threshold would leave a
 * cautious rider unable to reach most of the city. The budget lets them cross
 * one bad block without opening the door to ten, and showing the number is
 * what makes that a guarantee rather than a claim.
 */
export function StressBudget({ plan, units }: { plan: PlanResponse; units: Units }) {
  const { stress_budget_m: allowed, stress_budget_used_m: used, profile } = plan;

  if (allowed === 0) {
    return (
      <section className="budget">
        <h3>Stress exposure</h3>
        <p className="budget__hard">
          No budget — this profile refuses anything above LTS {profile.max_lts} outright.
          {used === 0 && ' The whole route stays at or below your comfort level.'}
        </p>
      </section>
    );
  }

  const pct = Math.min(100, (used / allowed) * 100);
  const tone = pct >= 90 ? 'is-bad' : pct >= 60 ? 'is-warn' : 'is-good';

  return (
    <section className="budget">
      <h3>Stress exposure</h3>
      <div className="budget__bar" role="img" aria-label={`${Math.round(pct)}% of the stress budget used`}>
        <div className={`budget__fill ${tone}`} style={{ width: `${pct}%` }} />
      </div>
      <p className="budget__text">
        <strong>{distance(used, units)}</strong> above LTS {profile.max_lts}, out of{' '}
        {distance(allowed, units)} allowed.
      </p>
      <p className="hint">
        Everything else on this route is at or below the traffic stress you said you were
        comfortable with.
      </p>
    </section>
  );
}
