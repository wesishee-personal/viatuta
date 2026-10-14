import { useState } from 'react';
import { Popup } from 'react-map-gl/mapbox';
import { ApiError, confirmHazard, resolveHazard } from '../api/client';
import type { HazardReport } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { compass, expiresIn, relativeTime } from '../lib/format';
import { useHazardKinds } from '../state/HazardKinds';

interface Props {
  hazard: HazardReport;
  onClose: () => void;
  /** Called after any response that changed the hazard, so the map refetches. */
  onConfirmed: () => void;
}

/** What to tell the rider when confirming or resolving fails. */
function explain(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 404) return 'That hazard has already expired or been taken down.';
    // 409 (your own report, or you already answered) and 429 (slow down) both
    // carry a message written for the rider.
    if (err.status === 409 || err.status === 429) {
      return err.message.charAt(0).toUpperCase() + err.message.slice(1) + '.';
    }
  }
  return "Couldn't send that. Try again in a moment.";
}

export function HazardPopup({ hazard, onClose, onConfirmed }: Props) {
  const { requireAuth } = useAuth();
  const { byKind } = useHazardKinds();
  const info = byKind(hazard.kind);

  const [busy, setBusy] = useState<'confirm' | 'resolve' | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  async function respond(kind: 'confirm' | 'resolve') {
    setBusy(kind);
    setFailure(null);
    try {
      if (kind === 'confirm') {
        await confirmHazard(hazard.id);
        setDone('Thanks — routing already knows.');
      } else {
        const res = await resolveHazard(hazard.id);
        // Resolving only takes the hazard down once enough riders agree, so
        // say which of the two happened.
        setDone(
          new Date(res.expires_at).getTime() <= Date.now()
            ? "Thanks — it's been taken down."
            : "Thanks. It comes down once one more rider agrees it's gone.",
        );
      }
      onConfirmed();
    } catch (err) {
      setFailure(explain(err));
    } finally {
      setBusy(null);
    }
  }

  // Directional kinds only affect riders going one way. Say which, so the
  // marker is not read as covering the whole street.
  const direction =
    info?.directional && hazard.heading_deg != null
      ? `Affects riders heading ${compass(hazard.heading_deg)}`
      : info?.directional
        ? 'Affects riders in both directions'
        : null;

  return (
    <Popup
      longitude={hazard.location.lon}
      latitude={hazard.location.lat}
      anchor="bottom"
      offset={12}
      closeOnClick={false}
      onClose={onClose}
      className="hazard-popup"
    >
      <h4>{info?.label ?? hazard.kind}</h4>
      {hazard.notes && <p className="hazard-popup__notes">{hazard.notes}</p>}
      {direction && <p className="hint">{direction}</p>}
      <p className="hint">
        Reported {relativeTime(hazard.created_at)} · {expiresIn(hazard.expires_at)}
      </p>
      <p className="hint">
        {hazard.confirmations === 0
          ? 'Nobody has confirmed this yet'
          : `Confirmed by ${hazard.confirmations} other rider${hazard.confirmations === 1 ? '' : 's'}`}
      </p>
      {done ? (
        <p className="hazard-popup__done">{done}</p>
      ) : (
        <div className="row">
          <button
            type="button"
            className="primary"
            disabled={busy !== null}
            onClick={() => requireAuth(() => void respond('confirm'), 'confirm a hazard')}
          >
            {busy === 'confirm' ? 'Confirming…' : "It's still there"}
          </button>
          <button
            type="button"
            disabled={busy !== null}
            onClick={() => requireAuth(() => void respond('resolve'), 'say a hazard is gone')}
          >
            {busy === 'resolve' ? 'Sending…' : "It's gone"}
          </button>
        </div>
      )}
      {failure && <p className="hint">{failure}</p>}
    </Popup>
  );
}
