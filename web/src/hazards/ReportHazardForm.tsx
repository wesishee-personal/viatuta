import { useState } from 'react';
import { Popup } from 'react-map-gl/mapbox';
import { reportHazard } from '../api/client';
import { HAZARD_KINDS, HAZARD_LABELS, HAZARD_NOTES_MAX } from '../api/types';
import type { HazardKind, LatLon } from '../api/types';

interface Props {
  location: LatLon;
  onCancel: () => void;
  onReported: () => void;
}

/**
 * Report a hazard at a dropped point.
 *
 * The body is built literally — `{location, kind, notes}` and nothing else.
 * The API decodes with DisallowUnknownFields, so an extra key is a 400.
 * (The Postman collection's `type`/`description` spelling is wrong; the Go
 * struct at internal/hazard/store.go:17 is the contract.)
 */
export function ReportHazardForm({ location, onCancel, onReported }: Props) {
  const [kind, setKind] = useState<HazardKind>('pothole');
  const [notes, setNotes] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const trimmed = notes.trim();
      await reportHazard(trimmed ? { location, kind, notes: trimmed } : { location, kind });
      onReported();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'could not submit the report');
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
        <label className="field">
          <span>What is it?</span>
          <select value={kind} onChange={(e) => setKind(e.target.value as HazardKind)}>
            {HAZARD_KINDS.map((k) => (
              <option key={k} value={k}>
                {HAZARD_LABELS[k]}
              </option>
            ))}
          </select>
        </label>
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
        {error && <p className="hint">{error}</p>}
        <div className="row">
          <button type="submit" className="primary" disabled={busy}>
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
