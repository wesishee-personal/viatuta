import { useState } from 'react';
import { Popup } from 'react-map-gl/mapbox';
import { confirmHazard } from '../api/client';
import type { HazardReport } from '../api/types';
import { HAZARD_LABELS } from '../api/types';
import type { HazardKind } from '../api/types';
import { expiresIn, relativeTime } from '../lib/format';

interface Props {
  hazard: HazardReport;
  onClose: () => void;
  onConfirmed: () => void;
}

export function HazardPopup({ hazard, onClose, onConfirmed }: Props) {
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [failed, setFailed] = useState(false);

  async function confirm() {
    setBusy(true);
    setFailed(false);
    try {
      await confirmHazard(hazard.id);
      setDone(true);
      onConfirmed();
    } catch {
      setFailed(true);
    } finally {
      setBusy(false);
    }
  }

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
      <h4>{HAZARD_LABELS[hazard.kind as HazardKind] ?? hazard.kind}</h4>
      {hazard.notes && <p className="hazard-popup__notes">{hazard.notes}</p>}
      <p className="hint">
        Reported {relativeTime(hazard.created_at)} · {expiresIn(hazard.expires_at)}
      </p>
      <p className="hint">
        {hazard.confirmations === 0
          ? 'Nobody has confirmed this yet'
          : `Confirmed by ${hazard.confirmations} other rider${hazard.confirmations === 1 ? '' : 's'}`}
      </p>
      {done ? (
        <p className="hazard-popup__done">Thanks — routing already knows.</p>
      ) : (
        <button type="button" className="primary" disabled={busy} onClick={() => void confirm()}>
          {busy ? 'Confirming…' : "It's still there"}
        </button>
      )}
      {failed && <p className="hint">Couldn't confirm — it may have already expired.</p>}
    </Popup>
  );
}
