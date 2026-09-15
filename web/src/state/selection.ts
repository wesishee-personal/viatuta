/**
 * Which slice of the route the rider is currently asking about.
 *
 * `kind` names the segment property to match on, so one selection type drives
 * both the traffic-stress legend and the bike-facility legend without the map
 * needing to know which one the click came from.
 */
export type Selection =
  | { kind: 'lts'; value: number }
  | { kind: 'infra'; value: string };

/** Legend keys are strings ("lts3"); segments carry the number. */
export function ltsKeyToValue(key: string): number {
  return key === 'unknown' ? 0 : Number(key.replace('lts', ''));
}

export function matches(sel: Selection | null, kind: Selection['kind'], value: string | number): boolean {
  return sel !== null && sel.kind === kind && sel.value === value;
}
