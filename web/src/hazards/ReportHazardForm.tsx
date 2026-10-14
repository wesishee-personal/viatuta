import { useState } from 'react';
import { Popup } from 'react-map-gl/mapbox';
import { ApiError, reportHazard } from '../api/client';
import { HAZARD_NOTES_MAX } from '../api/types';
import type { LatLon } from '../api/types';
import { span } from '../lib/format';
import { useHazardKinds } from '../state/HazardKinds';

/** Preferred starting choice, since it is the most common report. */
const DEFAULT_KIND = 'pothole';

interface Props {
  location: LatLon;
  onCancel: () => void;
  /** `merged` means the report joined a hazard already on the map. */
  onReported: (merged: boolean) => void;
}

/**
 * Report a hazard at a dropped point.
 *
 * The body is built literally — `{location, kind, notes}` and nothing else.
 * The API decodes with DisallowUnknownFields, so an extra key is a 400. No
 * heading is sent: a report made from the map is not tied to a direction of
 * travel, which the server reads as "affects both directions".
 */
export function ReportHazardForm({ location, onCancel, onReported }: Props) {
  const { status, kinds, byKind, retry } = useHazardKinds();
  const [chosen, setChosen] = useState<string | null>(null);
  const [notes, setNotes] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const kind = chosen ?? (byKind(DEFAULT_KIND) ? DEFAULT_KIND : (kinds[0]?.kind ?? ''));
  const info = byKind(kind);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!kind) return;
    setBusy(true);
    setError(null);
    try {
      const trimmed = notes.trim();
      const { merged } = await reportHazard(
        trimmed ? { location, kind, notes: trimmed } : { location, kind },
      );
      onReported(merged);
    } catch (err) {
      setError(
        err instanceof ApiError && err.status === 401
          ? 'Your session has expired. Sign in again to report.'
          : err instanceof Error
            ? err.message
            : 'could not submit the report',
      );
      setBusy(false);
    }
  }

  return (
    <Popup
      longitude={location.lon}
      latitude={location.lat}
      anchor="bottom"
      offset={12}
      closeOnClick={false}
      onClose={onCancel}
      className="hazard-popup"
    >
      <form onSubmit={(e) => void submit(e)}>
        <h4>Report a hazard here</h4>

        {status === 'loading' && <p className="hint">Loading hazard types…</p>}
        {status === 'error' && (
          <p className="hint">
            Couldn't load the hazard types.{' '}
            <button type="button" className="link" onClick={retry}>
              Try again
            </button>
          </p>
        )}

        {status === 'ready' && (
          <>
            <label className="field">
              <span>What is it?</span>
              <select value={kind} onChange={(e) => setChosen(e.target.value)}>
                {kinds.map((k) => (
                  <option key={k.kind} value={k.kind}>
                    {k.label}
                  </option>
                ))}
              </select>
            </label>
            {info && (
              <p className="hint">
                Stays on the map for about {span(info.ttl_s)}, longer if other riders confirm it.
                {info.closure && ' Routes will steer well clear of this road.'}
              </p>
            )}
            <label className="field">
              <span>
                Notes <em>(optional)</em>
              </span>
              <textarea
                value={notes}
                maxLength={HAZARD_NOTES_MAX}
                rows={2}
                placeholder="Glass across the whole lane"
                onChange={(e) => setNotes(e.target.value)}
              />
            </label>
          </>
        )}

        {error && <p className="hint">{error}</p>}
        <div className="row">
          <button type="submit" className="primary" disabled={busy || status !== 'ready'}>
            {busy ? 'Sending…' : 'Report'}
          </button>
          <button type="button" onClick={onCancel}>
            Cancel
          </button>
        </div>
        <p className="hint">Routing picks this up immediately — replan to see it take effect.</p>
      </form>
    </Popup>
  );
}
