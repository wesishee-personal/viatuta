import type {
  ErrorCode,
  ErrorResponse,
  HazardListResponse,
  HazardReport,
  HazardSubmission,
  HealthResponse,
  PlanRequest,
  PlanResponse,
} from './types';

/** Empty in development: the Vite proxy makes the API same-origin. */
const BASE = import.meta.env.VITE_API_BASE ?? '';

/** The server's write timeout is 60s (cmd/api/main.go:86) and a cautious
 *  plan across town is genuinely CPU-bound, so we give it the same budget
 *  rather than giving up on a slow-but-succeeding request. */
const TIMEOUT_MS = 60_000;

/**
 * A failed API call, carrying the backend's stable error envelope.
 *
 * Callers branch on `code` — never on `message`, whose wording the backend
 * explicitly reserves the right to change (internal/httpapi/errors.go:14).
 */
export class ApiError extends Error {
  readonly code: ErrorCode;
  readonly status: number;
  readonly details: Record<string, string>;
  /** Echoed by withRequestID (internal/httpapi/middleware.go:47) — worth
   *  showing the rider so a bug report can be traced to a server log line. */
  readonly requestId: string | null;

  constructor(status: number, body: ErrorResponse, requestId: string | null) {
    super(body.message);
    this.name = 'ApiError';
    this.code = body.code;
    this.status = status;
    this.details = body.details ?? {};
    this.requestId = requestId;
  }

  /** No safe path exists under the requested profile. The backend never
   *  silently relaxes the constraint, so this is a UI state, not a failure. */
  get isUnroutable(): boolean {
    return this.code === 'unroutable';
  }

  /** The routing graph is still loading at startup (~400ms after boot, but
   *  the process is up and serving health checks well before that). */
  get isWarmingUp(): boolean {
    return this.status === 503;
  }
}

/** A network failure, an abort, or a non-JSON response. */
export class NetworkError extends Error {
  constructor(message: string, readonly cause?: unknown) {
    super(message);
    this.name = 'NetworkError';
  }
}

async function request<T>(path: string, init: RequestInit, signal?: AbortSignal): Promise<T> {
  // Compose the caller's signal with our own timeout so either can cancel.
  const timeout = AbortSignal.timeout(TIMEOUT_MS);
  const composed = signal ? AbortSignal.any([signal, timeout]) : timeout;

  let res: Response;
  try {
    res = await fetch(BASE + path, { ...init, signal: composed });
  } catch (err) {
    if (signal?.aborted) throw err; // a deliberate cancel, not a failure
    if (timeout.aborted) throw new NetworkError('the server took too long to respond', err);
    throw new NetworkError('could not reach the server', err);
  }

  const requestId = res.headers.get('X-Request-ID');

  if (!res.ok) {
    let body: ErrorResponse;
    try {
      body = (await res.json()) as ErrorResponse;
    } catch {
      body = { code: 'internal_error', message: `request failed with status ${res.status}` };
    }
    throw new ApiError(res.status, body, requestId);
  }

  if (res.status === 204) return undefined as T;
  try {
    return (await res.json()) as T;
  } catch (err) {
    throw new NetworkError('the server sent a response we could not read', err);
  }
}

function postJSON<T>(path: string, body: unknown, signal?: AbortSignal): Promise<T> {
  return request<T>(
    path,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    },
    signal,
  );
}

/**
 * Plan a route.
 *
 * `req` must contain only the fields PlanRequest declares — the backend
 * decodes with DisallowUnknownFields, so an extra key is a 400 rather than
 * something harmlessly ignored. Build the object literally at the call site.
 */
export function planRoute(req: PlanRequest, signal?: AbortSignal): Promise<PlanResponse> {
  return postJSON<PlanResponse>('/v1/route/plan', req, signal);
}

/** Active hazards inside a bounding box. `bbox` is minLon,minLat,maxLon,maxLat. */
export function listHazards(bbox: string, signal?: AbortSignal): Promise<HazardListResponse> {
  const params = new URLSearchParams({ bbox });
  return request<HazardListResponse>(`/v1/hazards?${params}`, { method: 'GET' }, signal);
}

export function reportHazard(sub: HazardSubmission, signal?: AbortSignal): Promise<HazardReport> {
  return postJSON<HazardReport>('/v1/hazards', sub, signal);
}

export function confirmHazard(id: number, signal?: AbortSignal): Promise<HazardReport> {
  return request<HazardReport>(`/v1/hazards/${id}/confirm`, { method: 'POST' }, signal);
}

/** Reports whether the routing graph is loaded. Used to turn a 503 into a
 *  "warming up" state that resolves itself rather than a dead end. */
export function readyz(signal?: AbortSignal): Promise<HealthResponse> {
  return request<HealthResponse>('/readyz', { method: 'GET' }, signal);
}
