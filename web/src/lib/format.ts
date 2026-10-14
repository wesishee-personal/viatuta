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

/**
 * A span of time for a rider: "30 minutes", "3 hours", "7 days". Hazard
 * lifetimes run from an hour to months, so one unit does not fit them all.
 */
export function span(seconds: number): string {
  const mins = Math.round(seconds / 60);
  if (mins < 90) return mins === 1 ? '1 minute' : `${mins} minutes`;
  const hours = Math.round(mins / 60);
  if (hours < 48) return hours === 1 ? '1 hour' : `${hours} hours`;
  const days = Math.round(hours / 24);
  return days === 1 ? '1 day' : `${days} days`;
}

/** "expires in 2 hours" — a hazard that is about to lapse is weaker evidence.
 *  Lifetimes can be a few hours, so this must not round to whole days. */
export function expiresIn(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return '';
  const secs = (then - Date.now()) / 1000;
  if (secs < 60) return 'expiring now';
  if (secs >= 86_400 && secs < 2 * 86_400) return 'expires tomorrow';
  return `expires in ${span(secs)}`;
}

const COMPASS = [
  'north',
  'north-east',
  'east',
  'south-east',
  'south',
  'south-west',
  'west',
  'north-west',
];

/** A compass heading in degrees as a direction word: 90 is "east". */
export function compass(deg: number): string {
  const i = Math.round((((deg % 360) + 360) % 360) / 45) % 8;
  return COMPASS[i] ?? 'north';
}

export function coord(n: number): string {
  return n.toFixed(5);
}
