import type { Breakdown as BreakdownData } from '../api/types';
import { INFRA_COLORS, INFRA_LABELS, INFRA_ORDER, LTS_COLORS, LTS_LABELS } from '../map/constants';
import { distance, distanceShort, elevation } from '../lib/format';
import type { Units } from '../lib/format';
import type { Selection } from '../state/selection';
import { ltsKeyToValue, matches } from '../state/selection';

const LTS_ORDER = ['lts1', 'lts2', 'lts3', 'lts4', 'unknown'] as const;

interface Bar {
  key: string;
  /** What a segment's `lts` or `infra` property holds for this row. */
  value: string | number;
  metres: number;
  color: string;
  label: string;
}

function rows(
  source: Record<string, number>,
  order: readonly string[],
  colors: Record<string, string>,
  labels: Record<string, string>,
  toValue: (key: string) => string | number,
): Bar[] {
  return order
    .map((key) => ({
      key,
      value: toValue(key),
      metres: source[key] ?? 0,
      color: colors[key] ?? '#ccc',
      label: labels[key] ?? key,
    }))
    .filter((s) => s.metres > 0);
}

interface Props {
  data: BreakdownData;
  units: Units;
  selection: Selection | null;
  onSelect: (next: Selection | null) => void;
  /** False when the server sent no segments — the rows still render their
   *  totals, but there is nothing on the map to highlight. */
  interactive: boolean;
}

/**
 * How far the route ran at each stress level and on each kind of facility.
 *
 * The API reports these as totals, and the segments array says where each one
 * happened — so clicking a row here isolates those stretches on the map. This
 * is the evidence behind the safety score, and the reason the score is an
 * explanation rather than an assertion.
 */
export function Breakdown({ data, units, selection, onSelect, interactive }: Props) {
  const lts = rows(data.by_lts_m ?? {}, LTS_ORDER, LTS_COLORS, LTS_LABELS, ltsKeyToValue);
  const infra = rows(data.by_infra_m ?? {}, INFRA_ORDER, INFRA_COLORS, INFRA_LABELS, (k) => k);

  const toggle = (kind: Selection['kind'], value: string | number) => {
    if (matches(selection, kind, value)) {
      onSelect(null);
      return;
    }
    onSelect({ kind, value } as Selection);
  };

  return (
    <section className="breakdown">
      <h3>What you'll actually ride</h3>
      {interactive && (
        <p className="hint">Tap any row to find it on the map.</p>
      )}

      <StackedBar
        title="Traffic stress"
        bars={lts}
        kind="lts"
        units={units}
        selection={selection}
        onToggle={toggle}
        interactive={interactive}
      />
      <StackedBar
        title="Bike facilities"
        bars={infra}
        kind="infra"
        units={units}
        selection={selection}
        onToggle={toggle}
        interactive={interactive}
      />

      <dl className="facts">
        {data.climb_m > 0 && <Fact label="Climbing" value={elevation(data.climb_m, units)} />}
        {data.descent_m > 0 && <Fact label="Descent" value={elevation(data.descent_m, units)} />}
        {data.steep_m > 0 && (
          <Fact
            label="Steep sections"
            value={distance(data.steep_m, units)}
            hint="Gradient past the comfort deadband"
          />
        )}
        {data.crash_exposure_m > 0 && (
          <Fact
            label="Crash-heavy road"
            value={distance(data.crash_exposure_m, units)}
            hint="Distance weighted by historical cyclist crash pressure"
          />
        )}
        {data.hazard_m > 0 && (
          <Fact
            label="Near a reported hazard"
            value={distance(data.hazard_m, units)}
            hint="Within range of an active rider report"
          />
        )}
      </dl>
    </section>
  );
}

interface StackedBarProps {
  title: string;
  bars: Bar[];
  kind: Selection['kind'];
  units: Units;
  selection: Selection | null;
  onToggle: (kind: Selection['kind'], value: string | number) => void;
  interactive: boolean;
}

function StackedBar({
  title,
  bars,
  kind,
  units,
  selection,
  onToggle,
  interactive,
}: StackedBarProps) {
  const total = bars.reduce((sum, s) => sum + s.metres, 0);
  if (total === 0) return null;

  // A selection in the OTHER legend should not make this one look inactive —
  // they describe the same road from two angles, not two competing filters.
  const dimOthers = selection?.kind === kind;

  return (
    <div className="stack">
      <h4>{title}</h4>
      <div className="stack__bar">
        {bars.map((s) => {
          const on = matches(selection, kind, s.value);
          return (
            <button
              key={s.key}
              type="button"
              className={`stack__seg ${dimOthers && !on ? 'is-dimmed' : ''}`}
              style={{ width: `${(s.metres / total) * 100}%`, background: s.color }}
              title={`${s.label} — ${distance(s.metres, units)}`}
              aria-label={`${s.label}, ${distance(s.metres, units)}`}
              aria-pressed={on}
              disabled={!interactive}
              onClick={() => onToggle(kind, s.value)}
            />
          );
        })}
      </div>
      <ul className="stack__legend">
        {bars.map((s) => {
          const on = matches(selection, kind, s.value);
          return (
            <li key={s.key}>
              <button
                type="button"
                className={`stack__row ${on ? 'is-active' : ''} ${dimOthers && !on ? 'is-dimmed' : ''}`}
                aria-pressed={on}
                disabled={!interactive}
                onClick={() => onToggle(kind, s.value)}
              >
                <span className="swatch" style={{ background: s.color }} aria-hidden="true" />
                <span className="stack__name">{s.label}</span>
                <span className="stack__amount">{distanceShort(s.metres, units)}</span>
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function Fact({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <>
      <dt title={hint}>{label}</dt>
      <dd>{value}</dd>
    </>
  );
}
