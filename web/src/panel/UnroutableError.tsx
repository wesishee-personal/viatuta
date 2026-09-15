import { ApiError, NetworkError } from '../api/client';
import { PROFILE_NAMES } from '../api/types';
import type { ProfileName } from '../api/types';
import type { PlanSettings } from '../state/usePlan';

interface Props {
  error: ApiError | NetworkError;
  settings: PlanSettings;
  onRetryWith: (profile: ProfileName) => void;
}

/** The next profile up the tolerance ladder, or null at the top. */
function nextUp(current: ProfileName): ProfileName | null {
  const ladder: ProfileName[] = ['cautious', 'comfortable', 'confident'];
  const i = ladder.indexOf(current);
  if (i === -1 || i === ladder.length - 1) return null;
  return ladder[i + 1] ?? null;
}

/**
 * A failed plan, rendered as a state rather than a toast.
 *
 * The backend deliberately never relaxes a constraint behind the rider's
 * back — it returns the constraint that failed plus a hint. So the honest UI
 * is to show that hint and offer the relaxation as an explicit choice.
 */
export function UnroutableError({ error, settings, onRetryWith }: Props) {
  if (error instanceof NetworkError) {
    return (
      <section className="notice notice--error">
        <h3>Couldn't reach the router</h3>
        <p>{error.message}. Check that the API is running on :8080.</p>
      </section>
    );
  }

  if (error.isUnroutable) {
    const suggestion = settings.custom ? null : nextUp(settings.profile);
    return (
      <section className="notice notice--error">
        <h3>No route fits this profile</h3>
        <p>{error.message}</p>
        {error.details['hint'] && <p className="hint">{error.details['hint']}</p>}
        {suggestion && (
          <button type="button" className="primary" onClick={() => onRetryWith(suggestion)}>
            Try “{suggestion}” instead
          </button>
        )}
        {!suggestion && !settings.custom && (
          <p className="hint">
            Even the most tolerant profile can't connect these points — they may be on opposite
            sides of a gap in the network. Try moving a pin.
          </p>
        )}
        {settings.custom && (
          <div className="row">
            {PROFILE_NAMES.filter((n) => n !== 'shortest').map((n) => (
              <button key={n} type="button" onClick={() => onRetryWith(n)}>
                {n}
              </button>
            ))}
          </div>
        )}
      </section>
    );
  }

  return (
    <section className="notice notice--error">
      <h3>That didn't work</h3>
      <p>{error.message}</p>
      {Object.entries(error.details).map(([k, v]) => (
        <p key={k} className="hint">
          {k}: {v}
        </p>
      ))}
      {error.requestId && <p className="hint">Request {error.requestId}</p>}
    </section>
  );
}
