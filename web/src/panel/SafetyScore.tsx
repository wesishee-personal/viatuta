import type { PlanResponse } from '../api/types';
import { distance, duration } from '../lib/format';
import type { Units } from '../lib/format';

/** The score runs 0 (an unbroken arterial) to 100 (fully protected). */
function band(score: number): { label: string; className: string } {
  if (score >= 80) return { label: 'Protected most of the way', className: 'is-good' };
  if (score >= 60) return { label: 'Mostly calm streets', className: 'is-ok' };
  if (score >= 40) return { label: 'Mixed — some exposure', className: 'is-warn' };
  return { label: 'Significant traffic exposure', className: 'is-bad' };
}

export function SafetyScore({ plan, units }: { plan: PlanResponse; units: Units }) {
  const { label, className } = band(plan.safety_score);

  return (
    <section className={`score ${className}`}>
      <div className="score__number">
        <strong>{Math.round(plan.safety_score)}</strong>
        <span>/ 100</span>
      </div>
      <div className="score__meta">
        <p className="score__band">{label}</p>
        <p className="score__stats">
          {distance(plan.distance_m, units)} · {duration(plan.duration_s)}
        </p>
        {/* The backend assumes a flat 15 km/h (route_handler.go:16). Saying so
            is cheaper than having a rider trust a number that does not know
            about the hill it just routed them up. */}
        <p className="score__caveat">Time assumes a steady 15 km/h and ignores gradient.</p>
      </div>
    </section>
  );
}
