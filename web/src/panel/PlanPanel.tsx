import type { ApiError, NetworkError } from '../api/client';
import type { LatLon, PlanResponse, ProfileName } from '../api/types';
import type { Units } from '../lib/format';
import type { Selection } from '../state/selection';
import type { PlanSettings, PlanStatus } from '../state/usePlan';
import { BaselineCompare } from './BaselineCompare';
import { Breakdown } from './Breakdown';
import { ProfilePicker } from './ProfilePicker';
import { SafetyScore } from './SafetyScore';
import { SnapNotice, Warnings } from './Notices';
import { StressBudget } from './StressBudget';
import { UnroutableError } from './UnroutableError';

interface Props {
  origin: LatLon | null;
  destination: LatLon | null;
  status: PlanStatus;
  plan: PlanResponse | null;
  error: ApiError | NetworkError | null;
  settings: PlanSettings;
  onSettings: (next: PlanSettings) => void;
  onRetryWith: (profile: ProfileName) => void;
  units: Units;
  onUnits: (u: Units) => void;
  showGhost: boolean;
  onToggleGhost: (on: boolean) => void;
  selection: Selection | null;
  onSelect: (next: Selection | null) => void;
  reporting: boolean;
  onToggleReporting: () => void;
  onClear: () => void;
  onSwap: () => void;
}

export function PlanPanel(props: Props) {
  const { origin, destination, status, plan, error, settings, units } = props;

  return (
    <aside className="panel">
      <header className="panel__head">
        <div>
          <h1>Viatuta</h1>
          <p className="tagline">Safer cycling routes for Austin</p>
        </div>
        <button
          type="button"
          className="units"
          onClick={() => props.onUnits(units === 'metric' ? 'imperial' : 'metric')}
          title="Switch units"
        >
          {units === 'metric' ? 'km' : 'mi'}
        </button>
      </header>

      <Endpoints {...props} />

      <ProfilePicker
        settings={settings}
        onChange={props.onSettings}
        seed={plan?.profile ?? null}
      />

      <button
        type="button"
        className={`report-toggle ${props.reporting ? 'is-active' : ''}`}
        aria-pressed={props.reporting}
        onClick={props.onToggleReporting}
      >
        {props.reporting ? 'Cancel hazard report' : 'Report a hazard'}
      </button>

      <div className="panel__body">
        {status === 'idle' && <Intro hasOrigin={!!origin} hasDestination={!!destination} />}

        {status === 'warming-up' && (
          <section className="notice">
            <h3>Loading the Austin road graph</h3>
            <p>The server reads 175k nodes and 412k edges at startup. This retries itself.</p>
          </section>
        )}

        {status === 'loading' && <p className="loading">Finding the safest way…</p>}

        {status === 'error' && error && (
          <UnroutableError error={error} settings={settings} onRetryWith={props.onRetryWith} />
        )}

        {status === 'ready' && plan && (
          <>
            <SafetyScore plan={plan} units={units} />
            <Warnings warnings={plan.warnings} />
            <StressBudget plan={plan} units={units} />
            <Breakdown
              data={plan.breakdown}
              units={units}
              selection={props.selection}
              onSelect={props.onSelect}
              interactive={!!plan.segments?.length}
            />
            <BaselineCompare
              plan={plan}
              showGhost={props.showGhost}
              onToggleGhost={props.onToggleGhost}
              units={units}
            />
            <SnapNotice snapped={plan.snapped} units={units} />
            <p className="hint panel__footnote">
              {plan.edge_count} road segments · computed in {plan.compute_ms} ms
            </p>
          </>
        )}
      </div>
    </aside>
  );
}

function Endpoints({ origin, destination, onClear, onSwap }: Props) {
  return (
    <section className="endpoints">
      <ol>
        <li className={origin ? 'is-set' : ''}>
          <span className="endpoints__badge">A</span>
          {origin ? 'Start set' : 'Click the map to set your start'}
        </li>
        <li className={destination ? 'is-set' : ''}>
          <span className="endpoints__badge">B</span>
          {destination ? 'Destination set' : 'Click again to set your destination'}
        </li>
      </ol>
      {(origin || destination) && (
        <div className="row">
          <button type="button" onClick={onSwap} disabled={!origin || !destination}>
            Swap
          </button>
          <button type="button" onClick={onClear}>
            Clear
          </button>
        </div>
      )}
    </section>
  );
}

function Intro({ hasOrigin, hasDestination }: { hasOrigin: boolean; hasDestination: boolean }) {
  if (hasOrigin && !hasDestination) {
    return <p className="intro">Now pick where you're going.</p>;
  }
  return (
    <div className="intro">
      <p>
        Drop two pins and Viatuta finds the safest way between them — not the fastest, and not
        the shortest.
      </p>
      <p className="hint">
        Coverage is Austin only. Drag either pin to adjust; the locate button in the top right
        sets your start.
      </p>
    </div>
  );
}
