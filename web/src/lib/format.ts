export type Units = 'metric' | 'imperial';

const M_PER_MILE = 1609.344;
const M_PER_FOOT = 0.3048;

/** A distance in metres, rendered for a rider rather than for a debugger. */
export function distance(m: number, units: Units): string {
  if (units === 'imperial') {
    const miles = m / M_PER_MILE;
    if (miles < 0.1) return `${Math.round(m / M_PER_FOOT)} ft`;
    return `${miles.toFixed(miles < 10 ? 1 : 0)} mi`;
  }
  if (m < 1000) return `${Math.round(m)} m`;
  const km = m / 1000;
  return `${km.toFixed(km < 10 ? 1 : 0)} km`;
}

/** Short form for tight spaces like stacked-bar segment labels. */
export function distanceShort(m: number, units: Units): string {
  if (units === 'imperial') {
    const miles = m / M_PER_MILE;
    return miles < 0.1 ? `${Math.round(m / M_PER_FOOT)}ft` : `${miles.toFixed(1)}mi`;
  }
  return m < 1000 ? `${Math.round(m)}m` : `${(m / 1000).toFixed(1)}km`;
}

/** Elevation, which reads more naturally in feet than in miles. */
export function elevation(m: number, units: Units): string {
  return units === 'imperial' ? `${Math.round(m / M_PER_FOOT)} ft` : `${Math.round(m)} m`;
}

export function duration(seconds: number): string {
  const mins = Math.round(seconds / 60);
  if (mins < 60) return `${mins} min`;
  const hours = Math.floor(mins / 60);
  const rest = mins % 60;
  return rest === 0 ? `${hours} h` : `${hours} h ${rest} min`;
}

/** "2 days ago" — hazards are only useful if you can see how stale they are. */
export function relativeTime(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return '';
  const mins = Math.round((Date.now() - then) / 60_000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins} min ago`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `${hours} h ago`;
  const days = Math.round(hours / 24);
  return days === 1 ? 'yesterday' : `${days} days ago`;
}

/** "expires in 12 days" — a hazard that is about to lapse is weaker evidence. */
export function expiresIn(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return '';
  const days = Math.round((then - Date.now()) / 86_400_000);
  if (days <= 0) return 'expiring now';
  return days === 1 ? 'expires tomorrow' : `expires in ${days} days`;
}

export function coord(n: number): string {
  return n.toFixed(5);
}
