# Viatuta

A cycling route planner that optimises for **safety first**. Not distance,
not time — safety is the objective function, and everything else is a
constraint on it.

Coverage for v1 is Austin, TX. Go + PostgreSQL/PostGIS, with a React map
frontend in `web/`.

## Why this exists

Every mainstream routing engine treats cyclist safety as a mild preference
you can nudge. Here it is the whole point, which is why Viatuta contains its
own routing engine rather than wrapping Valhalla or GraphHopper — the cost
function is the product, so it has to be ours to tune.

See [docs/safety-model.md](docs/safety-model.md) for the reasoning behind
every parameter.

## Getting started

Requires Go 1.22+ and PostgreSQL 18 with PostGIS.

```bash
brew install postgresql@18 postgis
brew services start postgresql@18

# postgresql@18 is "keg-only", meaning Homebrew does not link its binaries
# into /opt/homebrew/bin. Without this, `psql` and `createdb` are not found.
echo 'export PATH="/opt/homebrew/opt/postgresql@18/bin:$PATH"' >> ~/.zshrc
export PATH="/opt/homebrew/opt/postgresql@18/bin:$PATH"

make tools      # install goose (migrations) and sqlc (SQL -> Go)
make db-create  # create the database, enable PostGIS
make migrate    # apply the schema
make run        # start the API on :8080

curl localhost:8080/healthz
curl localhost:8080/readyz   # also checks the database and graph
```

Then load the map data and plan a route:

```bash
make data       # download the Austin OSM extract (~65 MB)
make ingest     # build the routing graph (~9 s)
make score      # classify traffic stress (~8 s)
make data-all   # ...or all of the above plus crash records and elevation
make run        # the server loads the graph at startup (~400 ms)

curl -X POST localhost:8080/v1/route/plan \
  -H 'Content-Type: application/json' \
  -d '{"waypoints":[{"lat":30.2747,"lon":-97.7404},{"lat":30.2669,"lon":-97.7729}],
       "profile":"cautious"}'
```

### Accounts

```bash
export VIATUTA_JWT_SECRET="$(openssl rand -base64 32)"   # required for accounts

curl -X POST localhost:8080/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"correct horse battery staple"}'
# -> {"token": "...", "user": {...}}

curl localhost:8080/v1/routes -H "Authorization: Bearer $TOKEN"
```

Without a signing secret the account endpoints return 503 rather than signing
tokens with something guessable. Saved routes are recomputed server-side from
their waypoints and profile, so a stored route's safety score is always one
this service actually produced.

Add `"alternatives": 2` to get other routes worth considering, each
meaningfully different rather than a minor variation, with its own distance,
safety score and stress exposure so the trade is visible.

Profiles are `cautious`, `comfortable` (default), `confident`, and `shortest`.
Pass a `custom` weight object to tune any term per request.

Four signals feed the cost: OSM infrastructure tags, historical cyclist
crashes, junction and turn risk, and terrain. Riders can also report hazards
(`POST /v1/hazards`), which affect routing immediately.

Each profile pairs a comfort threshold (`max_lts`) with a **stress budget**:
how far above that threshold the rider will ride across the whole route.
Austin's low-stress network is islanded by arterials, so a hard threshold
leaves a cautious rider unable to reach most of the city; a bounded budget
lets them cross one bad block without opening the door to ten. The response
reports `stress_budget_used_m` so the exposure is visible rather than merely
promised.

Paste the returned `geometry` into <https://geojson.io> to see it on a map —
or run the frontend and watch it draw itself.

## Frontend

A React + TypeScript map that plans routes by dropping two pins. It exists to
make the safety model visible: the score, the stress budget the route actually
spent, what surfaces you will ride on, and how much detour safety charged
against the shortest path.

```bash
cp web/.env.example web/.env   # add a public Mapbox token (pk....)
make web-install
make run                       # the API, on :8080
make web-dev                   # the frontend, on :5173
```

The dev server proxies `/v1` to the API, so there is no CORS to configure and
no API URL to set. `make web-build` type-checks and builds into `web/dist`;
the server does not serve static files, so a deployed build is hosted
separately and reads `VITE_API_BASE`.

Mapbox rather than Google because the route geometry is OSM-derived and so is
Mapbox's basemap — the line lands on the road. Google's road geometry diverges
from OSM exactly where bike infrastructure is, and their terms require a
non-Google route on a Google map to be snapped to Google's network and
labelled as third-party.

### About the Mapbox token

The token is public by necessity. Vite inlines `VITE_*` variables at build
time, and the browser must send the token to Mapbox on every tile request, so
it is readable in `web/dist` and in the network tab. Proxying tiles through
this API to conceal it is not an option either — Mapbox's terms prohibit
proxying and intermediate caching of map content.

So secrecy is not the control. These are:

- **Restrict the token by URL** in the Mapbox dashboard — `http://localhost:5173`
  for development plus each deployed origin. An unrestricted token works from
  anyone's site, billed to you. This is the one that matters.
- **Create a token for this app.** The account's default public token cannot be
  scope-limited or URL-restricted.
- **Keep the default public scopes.** The frontend needs `styles:tiles`,
  `styles:read`, and `fonts:read` and nothing more.
- **Separate dev and production tokens**, so revoking one does not take down
  the other.

A secret (`sk.`) token in `VITE_MAPBOX_TOKEN` fails the build outright rather
than being compiled into a public bundle, and the dev server refuses to render
the map and tells you to revoke it.

`make help` lists everything else.

## Layout

```
cmd/api          HTTP server entry point
cmd/viatuta      operator CLI: data ingest, graph building, diagnostics
internal/
  config         environment -> Config
  geo            coordinates, distance, bearings, turn angles
  osm            .osm.pbf -> routable ways and nodes
  crash          TxDOT crash records -> per-edge crash pressure
  elevation      terrain tiles -> node elevation -> edge gradient
  hazard         rider-reported hazards, as a live overlay
  graph          the in-memory routing graph
  safety         ★ LTS, cost function, turn penalties, rider profiles
  routing        A* search and alternatives — knows nothing about bikes
  store          database pool + sqlc-generated queries
  auth           bcrypt password hashing, JWT issue and verify
  savedroute     routes a rider has kept
  httpapi        handlers, middleware, error shape
db/migrations    goose SQL migrations
web/             React + Vite frontend
  src/api        typed mirrors of the Go request/response structs
  src/map        Mapbox layers: route, baseline ghost, hazards, pins
  src/panel      the safety model, made legible
  src/state      plan and hazard fetching, debounced and cancellable
```

The split between `safety/` and `routing/` is deliberate and load-bearing:
`routing/` finds cheap paths through an abstract graph, `safety/` decides
what things cost. Retuning the safety model never touches search code.

## Status

- [x] **Phase 1** — module, config, logging, schema, health endpoints
- [x] **Phase 2** — OSM ingest (175k nodes, 412k directed edges, ~9 s)
- [x] **Phase 3** — in-memory graph + Dijkstra + `/v1/route/plan`
- [x] **Phase 4** — LTS, weighted cost, turn costs, rider profiles
- [x] **Phase 5** — crash records, elevation, rider hazard reports
- [x] **Phase 6** — accounts, tokens, saved routes
- [x] **Phase 7** — A*, alternative routes, allocation work
