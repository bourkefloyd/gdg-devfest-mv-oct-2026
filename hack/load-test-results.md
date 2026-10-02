# Word Rust Cloud Run — k6 smoke (2026-10-01 ~23:50 UTC)

**Target:** https://wordrust-958584846348.us-central1.run.app/  
**Tool:** k6 2.3.0  
**Scenario:** `smoke-arena-public` — 3 VUs, 30s (55 iterations); `GET /`, `POST /api/arena/runs` (bots, 2 players, 10s), brief `GET` on SSE events URL (5s timeout).  
**Note:** Repo `loadtest/s1-gateway.js` and `s2-real-hybrid.js` hit authenticated `/v1/games`; public Cloud Run arena uses **`/api/arena/runs`** (not `/arena/runs`). No API keys used.

## Pre-check (curl)

| Route | HTTP status |
|-------|-------------|
| `GET /` | **200** |
| `GET /healthz` | **404** (expected) |
| `POST /arena/runs` | **404** |
| `POST /api/arena/runs` | **202** |

**503 / redeploy:** setup saw **200** on `/`; no 503 retries were required.

## k6 results

| Metric | Value |
|--------|-------|
| **HTTP error rate** (`http_req_failed`) | **0.60%** (1 / 166 requests) |
| **p50 latency** (`http_req_duration`) | **72.3 ms** |
| **p95 latency** (`http_req_duration`) | **1048.6 ms** |
| **Check pass rate** | **99.5%** (219 pass / 1 fail) |

### Per-route checks (55 iterations)

| Check | Pass | Fail |
|-------|------|------|
| `GET /` → 200 | 55 | 0 |
| `POST /api/arena/runs` → 202 | 55 | 0 |
| Response includes `run_id` | 55 | 0 |
| Arena SSE events URL → 200 (5s cap) | 54 | 1 |

### Arena run actually starts?

**Yes.** All **55** arena POSTs returned **202** with a non-empty `run_id` and `events_url`. One SSE probe hit k6’s **5s request timeout** (long-lived stream); that accounts for the single failed HTTP request and the elevated p95.

## Script location

Ad-hoc script used for this run: `/tmp/oxidizinggemma/loadtest/smoke-arena-public.js` (clone: [bourkefloyd/oxidizinggemma](https://github.com/bourkefloyd/oxidizinggemma)).

---

## Post-deploy poll + N=24 arena (2026-10-01 ~23:54–23:55 UTC)

**Context:** Smoke above ran against the **pre–PR #19/#20** revision (per coordinator). `gcloud` is **not authenticated** in this environment (`gcloud run services describe` unavailable), so deploy readiness used the documented fallback: **10-seat mixed arena** and SSE `game_finished` **`profile`** fields from PR #19.

### Revision / deploy poll

| Method | Result |
|--------|--------|
| `gcloud run services describe wordrust … latestReadyRevisionName` | **Skipped** (no active `gcloud` auth) |
| Profile poll (`POST /api/arena/runs`, count=10, mixed, 30s) | **First check ~23:54 UTC:** HTTP **202**; profiles **`Gemini agent`**, **`Gemma agent (baseline)`**, **`Gemma agent (diffusion)`**, **`Gemma agent (diffusion JEV)`** — all four PR #19 names present → **treated as new revision live** (no further 1/min polling needed) |

### N=24 arena run

**Request:** `POST /api/arena/runs` with `{"count":24,"player_mix":"mixed","duration_s":30}`  
**Run ID:** `e0d74cec94546d50`  
**POST:** HTTP **202**, ~**141 ms**  
**429 rate limits:** **0**  
**Wall time (POST → `run_finished`):** ~**30.1 s**

#### Run-level stats (`run_finished` aggregate)

| Metric | Value |
|--------|--------|
| Games completed | 24 / 24 |
| Total words | 50 |
| Words/sec | 1.67 |
| **p50 latency (ms)** | **28,364** |
| **p95 latency (ms)** | **29,499** |
| **Errors** | **22** |
| Retries | 77 |

#### Per-profile scores (from `game_finished`)

| Profile | Games | Total score | Avg score | Word count | Games with error |
|---------|------:|------------:|----------:|-----------:|-----------------:|
| Gemini agent | 2 | 15,200 | 7,600 | 50 | 0 |
| Gemma agent (baseline) | 7 | 0 | 0 | 0 | 7 |
| Gemma agent (diffusion) | 7 | 0 | 0 | 0 | 7 |
| Gemma agent (diffusion JEV) | 8 | 0 | 0 | 0 | 8 |

**N=100:** Not run (N=24 completed with margin before 00:15 UTC; skipped optional soak per assignment).

**Harness:** `/tmp/arena_poll_and_n24.py` (`poll` + `n24` modes).
