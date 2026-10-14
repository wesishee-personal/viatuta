import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { listHazardKinds } from '../api/client';
import type { HazardKindInfo } from '../api/types';

type Status = 'loading' | 'ready' | 'error';

interface HazardKindsValue {
  status: Status;
  /** In the order the server lists them, which is the order to display. */
  kinds: HazardKindInfo[];
  /** Look a kind up by name. Undefined for a kind this client has not heard of. */
  byKind: (kind: string) => HazardKindInfo | undefined;
  retry: () => void;
}

const HazardKindsContext = createContext<HazardKindsValue | null>(null);

export function useHazardKinds(): HazardKindsValue {
  const value = useContext(HazardKindsContext);
  if (!value) throw new Error('useHazardKinds must be used inside <HazardKindsProvider>');
  return value;
}

/**
 * Loads the hazard kinds once and shares them. The server owns the list (and
 * each kind's lifetime), so the map, the report form and the popup all read it
 * from here and a new kind needs no web release.
 */
export function HazardKindsProvider({ children }: { children: ReactNode }) {
  const [kinds, setKinds] = useState<HazardKindInfo[]>([]);
  const [status, setStatus] = useState<Status>('loading');
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const ctrl = new AbortController();
    setStatus('loading');
    listHazardKinds(ctrl.signal)
      .then((res) => {
        setKinds(res.kinds);
        setStatus('ready');
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setStatus('error');
      });
    return () => ctrl.abort();
  }, [attempt]);

  const retry = useCallback(() => setAttempt((n) => n + 1), []);

  const value = useMemo<HazardKindsValue>(() => {
    const index = new Map(kinds.map((k) => [k.kind, k]));
    return { status, kinds, byKind: (kind) => index.get(kind), retry };
  }, [kinds, status, retry]);

  return <HazardKindsContext.Provider value={value}>{children}</HazardKindsContext.Provider>;
}
