# Hazard reporting — spec

Status: **the backend is implemented** (migrations 00007 to 00009). The iOS and
web clients do not use the new features yet; see "Remaining work" at the end.

Riders report hazards from two places in the iOS app — the route-planning view
and (later) the live-navigation view. Hazards are stored in Postgres, expire
on a per-kind schedule, and steer routing through the in-memory overlay
described in [safety-model.md](safety-model.md).

## Decisions

| # | Decision |
|---|----------|
| 1 | Each kind has its own lifetime, confirmation extension, and maximum life (table below). |
| 2 | `road_closed` is **aggressively nudged, never hard-avoided**. The penalty grows with the number of reports. |
| 3 | Every write is tied to a signed-in user. Anonymous reporting is not allowed. |
| 4 | New kinds: `glass`, `road_closed`, `flooding`, `fallen_tree`, `signal_out`, `street_event`, `slippery_surface`, `loose_animal`, plus `accident`. |
| 5 | Duplicate reports merge: **50 m** for area hazards, **25 m** for place-specific ones. |
| 6 | Reports record a **heading**. Planning-view reports are bidirectional; navigation reports use the rider's direction of travel. The rider never has to enter a direction. |

## Kinds and lifetimes

Lifetime is measured from the report. Each confirmation adds the listed time,
but a hazard can never live past its max life.

| Kind | Starting life | Each confirmation adds | Max life | Merge radius | Heading used in routing |
|---|---|---|---|---|---|
| `accident` | 3 h | +1 h | 8 h | 50 m | yes |
| `road_closed` | 24 h | +12 h | 7 d | 50 m | no |
| `construction` | 24 h | +12 h | 7 d | 50 m | no |
| `flooding` | 12 h | +6 h | 48 h | 50 m | no |
| `poor_visibility` | 12 h | +6 h | 3 d | 50 m | yes (glare is directional) |
| `aggressive_traffic` | 6 h | +3 h | 24 h | 50 m | yes |
| `signal_out` | 4 h | +2 h | 24 h | 50 m | no |
| `street_event` | 12 h | +6 h | 3 d | 50 m | no |
| `dangerous_junction` | 30 d | +30 d | 180 d | 50 m | no |
| `pothole` | 7 d | +7 d | 60 d | 25 m | yes |
| `glass` | 3 d | +3 d | 14 d | 25 m | yes |
| `debris` | 2 d | +2 d | 14 d | 25 m | yes |
| `slippery_surface` | 12 h | +6 h | 3 d | 25 m | yes |
| `blocked_lane` | 3 h | +2 h | 12 h | 25 m | yes |
| `fallen_tree` | 24 h | +12 h | 7 d | 25 m | no |
| `loose_animal` | 1 h | +30 min | 4 h | 25 m | no |
| `other` | 24 h | +12 h | 7 d | 25 m | no |

`accident` is deliberately not called `collision`: that name belongs to the
historical crash data.

Lifetimes, radii and the directional flag live in one Go registry
(`internal/hazard`), which is the single source of truth. The CHECK constraint
on `hazard_report.kind` is widened by migration to match, and the app reads the
registry through `GET /v1/hazards/kinds` rather than hard-coding it.

## Direction

- `hazard_report.heading_deg` is nullable, `0 <= heading_deg < 360`.
  **NULL means bidirectional.**
- **Planning view:** never sends a heading, so the hazard is bidirectional.
- **Navigation view:** the app sends its best estimate of the direction the
  rider was travelling, in this order:
  1. the bearing of the active route at the rider's position;
  2. otherwise GPS course, if speed is at least about 2 m/s and the course is
     valid;
  3. otherwise nothing (bidirectional).
- The heading is **stored for every kind** but **used in routing only for kinds
  marked "yes"** above. Changing which kinds are directional later needs no
  data migration.
- **Routing.** The graph is directed (each direction of a road is its own
  edge, with bearings). A directional hazard affects an edge only when the
  edge's bearing is within 90 degrees of the heading. The overlay build adds
  this bearing check when it matches hazards to edges.
- **Merging.** Two reports can merge only if their directions are compatible:
  either is NULL, or they differ by at most 90 degrees. A pothole reported
  from the opposite direction is treated as a separate hazard, since it is
  probably in the other lane.

## Merging duplicates

A report of the same kind within the kind's merge radius of an active hazard,
with a compatible direction, is **not** stored as a new row. It counts as a
confirmation by that rider, and the API returns the existing hazard (HTTP 200
rather than 201). If that rider has already voted on it, nothing changes.

## Confirm, and "no longer there"

- `POST /v1/hazards/{id}/confirm` and `POST /v1/hazards/{id}/resolve`.
- **One vote per user per hazard**, enforced by a `hazard_vote` table with a
  unique `(hazard_id, user_id)`. The original reporter counts as the first
  confirmation and cannot confirm their own report again (409).
- **Resolve:** two riders voting "no longer there", or the original reporter
  alone, expires the hazard immediately.
- Confirmation extends life by the per-kind amount, capped at the max life.
  This replaces today's flat +14 days.

## Road closures

`road_closed` does not use the ordinary hazard score, whose largest possible
penalty is about +2x the edge length. It applies to roads within 20 m of the
report (tighter than the 30 m ordinary hazards use), so a closure does not shut
the crossing streets at a junction. It has its own, much steeper channel:

```
n       = 1 + distinct confirming riders
penalty = min(150, 15 * n^1.5)      # added to the edge cost multiplier
```

| Reports | Extra cost on that road |
|---|---|
| 1 | +15x its length |
| 2 | +42x |
| 3 | +78x |
| 4 | +120x |
| 5+ | +150x (cap) |

- **Never infinite.** If the closure is wrong, or it is the only way through,
  the router can still use the road.
- **Ignores the rider profile**, including `shortest`. A closure is a fact
  about the road, not a comfort preference.
- **Does not fade** with age. It is full strength until it expires or is
  resolved.
- If the chosen route still crosses an active closure, the plan response
  includes a warning.
- These numbers are starting values to tune against the Austin graph. In a
  live check on a 7 km Austin trip, a single report was already enough to
  reroute around a closure at every spot tried (detours of up to 1.1 km), so
  more reporters only change the outcome where the alternative is much longer
  than about 2 km. If one unverified report should be a gentler nudge, lower
  `closureBase` in `internal/hazard/hazard.go`.

## Authentication

- `POST /v1/hazards`, `POST /v1/hazards/{id}/confirm` and
  `POST /v1/hazards/{id}/resolve` require a bearer token (401 otherwise).
- `GET /v1/hazards` and `GET /v1/hazards/kinds` stay public: planning a route
  needs no login, and neither does seeing what is on the map.
- `hazard_report.user_id` stays nullable at the database level, because its
  foreign key is `ON DELETE SET NULL`. Deleting an account keeps the safety
  data and drops the author. The API enforces that new reports always have one.

## Rate limiting

In-memory token bucket per signed-in user (`golang.org/x/time/rate`), applied
after auth on the hazard write routes:

- reports: 20 per hour, burst of 5;
- confirms and resolves: 60 per hour, burst of 10.

Over the limit returns `429` with `Retry-After` and a new `rate_limited` error
code. Idle limiters are evicted after an hour. The limiter resets on restart
and is per API instance, which is fine for a single container; move it to
Postgres or Redis if the API is ever scaled out.

## API summary

| Endpoint | Auth | Notes |
|---|---|---|
| `POST /v1/hazards` | required | Body: `location`, `kind`, optional `notes`, `heading_deg`, `client_id`. 201 for a new hazard, 200 when merged. |
| `GET /v1/hazards?bbox=` | public | Active hazards in a box. |
| `GET /v1/hazards/kinds` | public | Label, lifetime, merge radius, directional flag per kind. |
| `POST /v1/hazards/{id}/confirm` | required | 409 if already voted. |
| `POST /v1/hazards/{id}/resolve` | required | May expire the hazard. |

`client_id` is generated by the app and makes retries idempotent, so a report
queued offline cannot be stored twice.

## Server work beyond the endpoints

- **Periodic overlay refresh, once a minute.** The overlay is rebuilt on every
  report, confirmation and resolve, and also every minute, so a hazard that
  expires stops steering routes within a minute. A rebuild over the 412k-edge
  Austin graph takes about 50 ms.
- Expired and resolved rows stay in the table as history.
- Direction is applied in the overlay query: a directional hazard scores an
  edge only when the edge's bearing, read at the end nearer the hazard, is
  within 90 degrees of the hazard's heading.

## iOS architecture

Planning and navigation share everything except where the location comes from.

- **`HazardService`:** one app-wide object, injected through the environment,
  with `report`, `confirm`, `resolve` and `nearby`. It knows nothing about
  screens. The caller supplies the coordinate and (optionally) the heading.
- **Kind picker:** one component, two modes.
  - *Detailed (planning):* pick the spot with a long-press or the map centre,
    choose a kind, optional note. No heading.
  - *Quick (navigation):* big buttons, no keyboard. One tap reports at the
    rider's position, backdated a few seconds, with a 5-second Undo and the
    estimated heading.
- **Shared map layer:** draws markers, and a sheet to confirm or resolve.
  Written once, used by both map views.
- **Offline outbox (navigation):** reports made without signal are queued on
  the device and retried with their `client_id`. A 429 counts as "retry later".
- Kinds, labels and lifetimes come from `GET /v1/hazards/kinds`.
- Later: a "hazard ahead" alert in navigation. Nothing here blocks it.

## Phases

1. **Backend:** kind registry and migration, per-kind lifetimes and confirm
   extension, `heading_deg`, direction-aware overlay, closure channel,
   merging, votes, resolve, rate limiting, `kinds` endpoint, periodic refresh,
   tests.
2. **iOS, planning view:** `HazardService`, kinds fetch, map layer, picker.
3. **iOS, navigation view:** quick mode and outbox, when that view exists.

## Remaining work

- **The iOS app and the web app do not use any of this yet.** Both still
  hard-code the original eight kinds. They should read `GET /v1/hazards/kinds`,
  and the iOS app needs the hazard service, map layer and picker described
  above. The web app also needs a "no longer there" button.
- The web app has had a sign-in flow since hazard writes began requiring an
  account.
- Routing weights for the newer kinds are first guesses, to be tuned against
  real routes.
- The rate limiter and the closure penalty are in memory and per instance,
  which is fine for the single API container.
- The Postman collection's Hazards folder is current. Its "Planned" folder
  still lists the auth and saved-route endpoints as not yet implemented.
