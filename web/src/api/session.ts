/**
 * Where the sign-in token lives between page loads.
 *
 * localStorage is the pragmatic choice for a browser client that talks to a
 * bearer-token API: the alternative, an httpOnly cookie, would need backend
 * changes (cookie issuing, CSRF protection). The trade-off is that any script
 * running on this origin can read the token, so keep third-party scripts off
 * the page. Tokens expire on their own (see internal/auth/token.go).
 *
 * Every access is wrapped: storage throws in private windows and when site
 * data is blocked, and the app must still work, just without remembering you.
 */
const KEY = 'viatuta.token';

let memory: string | null = null;

export function getToken(): string | null {
  try {
    return localStorage.getItem(KEY) ?? memory;
  } catch {
    return memory;
  }
}

export function setToken(token: string): void {
  memory = token;
  try {
    localStorage.setItem(KEY, token);
  } catch {
    /* kept in memory for this tab only */
  }
}

export function clearToken(): void {
  memory = null;
  try {
    localStorage.removeItem(KEY);
  } catch {
    /* nothing to clear */
  }
}
