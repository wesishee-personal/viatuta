# The Safety Model

This document is the reasoning behind every number in
`internal/safety/params.go`. If you change a constant there, change the
justification here. A safety model nobody can explain is a safety model
nobody should trust.

## The core claim

A route's cost is **effective distance**: how long the route would need to be
if it were entirely protected bike track to feel equally bad.

```
edge_cost_meters = length_m × multiplier
multiplier       = 1.0 + Σ (weight_i × penalty_i)
```

The multiplier is never below 1.0. That gives two properties we depend on:

1. **Interpretability.** A 5 km route costing 9,000 means "as unpleasant as
   9 km of protected track." You can explain that number to a rider.
2. **A correct A\* heuristic.** A\* requires an estimate that never exceeds
   the true remaining cost. Because no edge can cost less than its own
   length, straight-line distance is always a valid underestimate. This is
   not a happy accident — it is why the floor of 1.0 is a hard rule and not a
   tuning choice.

## Level of Traffic Stress (LTS)

LTS is the backbone of the model. It is not our invention: it comes from
Mekuria, Furth & Nixon (2012), and is used by transportation departments to
classify how much stress a road puts on a cyclist.

| LTS | Meaning | Typical road |
|-----|---------|--------------|
| 1 | Suitable for children | Protected track, quiet residential street, off-street path |
| 2 | Most adults tolerate it | Bike lane on a 30 mph, 2-lane road |
| 3 | Confident cyclists | Bike lane on a busy multi-lane arterial |
| 4 | Fearless riders only | Mixed traffic on a high-speed arterial, no facility |

Classification uses, in order: the presence and type of a bike facility, the
number of through lanes, and the speed limit. A protected track is LTS 1
regardless of the road it runs beside — that is the entire point of physical
separation.

**Why LTS rather than a raw crash rate?** Crash data is *exposure-blind*. A
terrifying arterial with no crashes on record may simply be a road no cyclist
is willing to ride. Counting crashes alone would score it as safe. LTS
measures the road itself, so it does not have that blind spot.

## Crash pressure

Crash history is the empirical counterweight to LTS's theoretical rubric —
it catches the specific junction that is worse than its geometry suggests.

- Source: TxDOT CRIS, via data.austintexas.gov.
- Only cyclist-involved crashes count.
- Severity is weighted on the KABCO scale; a fatality counts far more than a
  possible injury.
- Recency decays exponentially — road layouts change, and a 2015 crash on a
  street that has since been rebuilt says little about today.
- The result is normalised to 0..1 per edge, so one catastrophic junction
  cannot arbitrarily dominate the whole cost function.

**Known limitation:** crash records are reported at the nearest address or
intersection, so positional accuracy is roughly ±30 m. We therefore treat
crash pressure as a neighbourhood signal, not a per-metre one.

### What the data gave us (2,350 records, 2016 onward)

The first run scored LTS 1 as *more* dangerous than LTS 2, which is
impossible if the model is right. The cause was instructive: protected tracks
running alongside arterials were inheriting the roadway's crash record, since
a crash on South Congress falls within the match radius of the separated track
beside it. A motor-vehicle collision in the carriageway is weak evidence about
the facility built to keep riders out of it.

Two corrections fixed the inversion:

- **Distance decay** (e-folding at 12 m), so a crash is evidence mainly about
  the segment it sits on rather than smearing equally across every nearby one.
- **A discount for separated infrastructure** (0.25), partial rather than
  total, because riders genuinely are struck on tracks at driveway and
  junction crossings.

Mean crash score by stress level, before and after:

| LTS | before | after |
|-----|--------|-------|
| 1 | 0.0453 | 0.0078 |
| 2 | 0.0112 | 0.0041 |
| 3 | 0.0490 | 0.0201 |
| 4 | 0.0597 | **0.0229** |

The residual LTS 1 > LTS 2 gap is expected rather than a defect: protected
tracks are built where traffic is heaviest, so they sit in high-exposure
corridors and carry real crossing conflicts.

## Intersection and turn risk

Most cyclist injuries happen at junctions rather than mid-block, so turn cost
is a first-class term, not a rounding adjustment.

The router's search state is *the directed edge you arrived on*, which means
when expanding to the next edge we know both the incoming and outgoing
direction and can charge for the specific movement:

| Movement | Cost |
|----------|------|
| Continue straight on a protected facility | ~0 |
| Signalised crossing | small |
| Right turn | small |
| Left turn at a signal | moderate |
| Left turn across unsignalised traffic | large, scaled by the crossed road's LTS |
| Crossing an arterial with no control | large |

Turn costs are expressed in the same "effective meters" unit as edges, so the
two are directly comparable and the totals stay meaningful.

## Secondary signals

- **Grade.** Penalised asymmetrically: a climb is tiring, a steep descent is
  genuinely hazardous on a bike. Below ~3% the penalty is zero.
- **Surface.** Gravel and dirt cost more, considerably more in the wet.
- **Lighting.** Applied *only* when the request is for a night-time ride.
  A `lit` value of NULL means unknown, which we treat as neither lit nor
  unlit rather than assuming the worst.
- **Hazard reports.** Rider-submitted, decaying toward a 90-day expiry, and
  weighted by how many other riders confirmed them.

  These are the only live signal in the system: everything else is refreshed
  by a batch job, but a rider reporting broken glass expects it to matter now.
  Scores therefore live in an in-memory overlay that is replaced atomically,
  and each request takes one immutable snapshot — so the search loop needs no
  locking, and a route cannot change shape halfway through being computed.

  The weighting is held below crash pressure deliberately. A report is
  unverified and a single person can file one, so hazards nudge routing rather
  than dictate it; corroboration from other riders is what raises their
  influence.

## Rider profiles

| Profile | MaxLTS | Character |
|---------|--------|-----------|
| `cautious` | 1 | Riding with kids. Will accept a long detour for separation. |
| `comfortable` | 2 | The default. Prefers facilities, tolerates quiet streets. |
| `confident` | 3 | Will take a bike lane on an arterial to save real time. |

`MaxLTS` is a **hard filter**, not a weight: edges above it are removed from
the graph entirely. The API also accepts a custom weight object, so the
presets are defaults rather than limits.

## Guardrails

Optimising for safety without bounds produces absurd results — a 3 km trip
routed 20 km around every arterial. Two constraints prevent this:

- `MaxDetourRatio` caps route length relative to the shortest path.
- When no route satisfies the profile, the API returns `unroutable` with the
  constraint that failed, rather than silently relaxing it. A rider who asked
  for LTS 1 must never be handed an LTS 4 arterial without being told.

## What the model actually produced (Austin, September 2026)

Classifying all 412,162 directed edges:

| Level | Edges | km | Share of distance |
|-------|-------|-----|------|
| LTS 1 | 49,471 | 5,261 | 13.2% |
| LTS 2 | 308,728 | 29,336 | 73.4% |
| LTS 3 | 26,424 | 2,682 | 6.7% |
| LTS 4 | 27,539 | 2,684 | 6.7% |

LTS 2 dominates because Texas's statutory 30 mph residential default lands
there. That is the rubric working as designed, not a bug — but it means almost
no Austin street qualifies as LTS 1, and the LTS 1 network is essentially just
the trail system.

### The connectivity finding

Measuring the largest connected component using only edges at or below each
threshold:

| Rider tolerance | Largest reachable component | Components |
|---|---|---|
| LTS <= 1 | **1.14%** of the graph | 151,278 |
| LTS <= 2 (cautious) | **18.91%** | 21,066 |
| LTS <= 3 (comfortable) | 68.12% | 11,016 |
| LTS <= 4 (confident) | 95.32% | 2,340 |

This is the central result, and it is a fact about Austin rather than about
the code: **the low-stress network is islanded.** Good LTS 1-2 neighbourhoods
exist, separated from each other by arterial barriers. Revealing exactly this
is what LTS analysis was invented for.

The practical consequence is that `MaxLTS` as a hard filter makes the
`cautious` profile return "unroutable" for many ordinary trips. That is what
the stress budget exists to solve.

## The stress budget

A hard threshold asks the wrong question. Riders do not refuse all stressful
road; they refuse *a lot* of it. Someone who will not ride two miles of Lamar
will happily cross it at a light to reach the neighbourhood beyond.

So `MaxLTS` is no longer a wall. It is a comfort threshold, paired with
`StressBudgetM`: how many metres above that threshold the rider will accept
across the whole route. Over-threshold road is permitted, charged a punishing
`OverThresholdPenalty` so the router uses as little as possible, and bounded
so exposure can never exceed what the rider allowed.

Setting `StressBudgetM` to zero restores the old hard filter exactly, so the
strict behaviour remains available.

### How it is enforced

The search state gains a second dimension: the edge you arrived on *and* how
much budget you have spent getting there. The same edge reached having spent
nothing and having spent 350 m are different situations — one can still afford
a crossing ahead, the other cannot — so they are explored separately.

Tracking this exactly is the resource-constrained shortest path problem, which
is NP-hard. The budget is therefore discretised into four buckets, making the
search exact with respect to the bucketed budget. The *exact* metres are
carried alongside and checked before any state is created, so the bound is
real: a route can never exceed the budget, only be marginally more
conservative than strictly necessary.

### Choosing the budget (measured, Austin, 40 random trips)

For a cautious rider with `MaxLTS = 2`:

| Budget | Trips routed | Mean budget actually used | Mean safety score |
|--------|--------------|---------------------------|-------------------|
| 0 m (hard filter) | 35% | 0 m | 95.2 |
| 200 m | 72% | 63 m | 94.2 |
| **400 m (default)** | **85%** | **116 m** | **93.5** |
| 800 m | 85% | 136 m | 93.2 |
| 1600 m | 92% | 236 m | 92.6 |
| 3200 m | 98% | 337 m | 92.3 |

Two things justify the 400 m default. It strictly dominates 800 m — identical
coverage, better safety — and it is a promise that can be stated plainly:
*you will never ride more than a quarter mile of road above your comfort
level.*

The mean-used column is the more important result. Even at a 3200 m budget the
router spends only 337 m on average, because the penalty makes over-threshold
road a genuine last resort. The budget bounds the worst case; it does not
invite spending.

## A note on the search

**A\* is correct here because of the multiplier floor.** The heuristic is
straight-line distance to the goal, which is only valid if it never
overestimates what remains to be paid. Since every edge costs at least its own
length — the cost multiplier can never drop below 1.0 — that holds by
construction. It is also consistent, so each state is settled exactly once.

This is not a property to take on trust: an inadmissible heuristic does not
crash or report an error, it quietly returns a worse route. `astar_test.go`
therefore runs A\* and unguided Dijkstra against the same random graphs and
requires identical costs, under length-only, safety-shaped, and budgeted cost
functions alike.

### What the measurements said

On a synthetic grid, A\* explored 159,029 states against Dijkstra's 159,190 —
no benefit at all, and 19% slower for the overhead. On the real Austin network
it explores 336,058 against 541,351 and runs in 58.8 ms against 90.5 ms.

The difference is structure. A uniform grid offers many equally good paths, so
there is nothing for the heuristic to rule out; a street network has dead
ends, river crossings and arterial barriers, and ruling those out is exactly
what a heuristic does. **Benchmarking a router on synthetic geometry would
have produced the wrong conclusion.**

Separately, the priority queue originally used `container/heap`, whose `any`
parameter boxes every pushed item — one allocation per push, and a search
performs hundreds of thousands. Writing the sift operations out directly took
a search from **391,297 allocations to 9**, and 6.4 MB to 154 KB. The search
workspace is pooled on top of that, so repeated requests reuse the same
arrays rather than allocating 28 MB each.

## Alternative routes

The primary route is found, its edges are made artificially expensive, and the
search runs again. A candidate is kept only if it shares less than 70% of its
length with an accepted route and costs no more than 1.6x the best — a route
twice as bad is not a choice, it is noise.

Reported costs are recomputed without the penalty, so the numbers a rider
compares are real. Measured on Austin, alternatives share between 1.8% and
12.7% of their geometry with the primary: genuinely different roads.

This matters more for a safety-first router than a conventional one. When the
safest route is a long detour, the rider deserves to see the trade rather than
simply be sent the long way.

## A note on accounts

Two decisions in `internal/auth` are worth knowing, because both look like
over-caution until they are not:

- **Login replies identically** for an unknown address and a wrong password,
  and deliberately hashes a dummy password when the address is unknown. Both
  are needed: the message alone would still leak the answer through response
  time, since a real lookup pays for bcrypt and a missing one would not.
  Measured difference between the two cases: 0.0 ms of 178 ms.

- **The token's algorithm is pinned to HS256.** A parser that trusts the
  header's own `alg` field accepts a token claiming `alg: none`, which is an
  attacker-authored identity with no signature at all. There is a test for it.

JWTs cannot be revoked before they expire; that is the cost of not consulting
the database on every request. Tokens last 24 hours. Anything longer wants a
refresh flow or server-side sessions instead.

## A note on identifiers

Anything computed in SQL is keyed by `routing_edge.id`. The in-memory graph
re-sorts edges by source node, so its indices are **different numbers**.
Translating between them is mandatory, and getting it wrong is silent: the two
id spaces overlap, so a mistranslated lookup lands on a real edge and simply
attributes the data to the wrong road. Use `Graph.IndexOfDBID`.

## Open questions

- How steeply should crash weight decay with age? Start: 5-year window,
  exponential half-life of ~2 years. Revisit once the data is loaded.
- Should `lit` default to penalised in areas where OSM coverage of street
  lighting is known to be sparse? Currently no; unknown stays neutral. At 1.1%
  tag coverage this term does almost nothing until a streetlight dataset is
  imported.

- **Should the budget apply per leg or per journey?** It is currently per leg,
  so a three-waypoint trip may spend it twice. That reads a via point as "take
  me through here", making each stage its own trip — defensible, but a rider
  might reasonably expect one budget for the whole outing.

- **Is 400 m the right default?** It routes 85% of trips. Raising it to 1600 m
  reaches 92% and costs under a point of mean safety score, which is a real
  argument. The counter-argument is that a mile of stressful road stops being
  a "cautious" route whatever the average says.
