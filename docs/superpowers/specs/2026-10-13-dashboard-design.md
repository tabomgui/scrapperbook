# Dashboard — Design

**Goal:** A web UI so the user can browse listings, manage searches/regions, and check system health without `curl`/`psql`. This is the "Plano 4 (dashboard): Next.js" item both plan 1 and plan 2 flagged as future roadmap, now that plan 3's `/api/v1` exists to build it against.

**Context:** The backend (`backend/`) already exposes everything this needs: `GET/POST/PATCH/DELETE /api/v1/searches`, `GET/POST/PATCH /api/v1/regions`, `GET /api/v1/listings(+/{id})`, `GET /api/v1/jobs|workers|sessions`, `POST /api/v1/sessions/{source}/resume`, `GET /api/v1/stats`. No auth on that API today (single-user, bound to `127.0.0.1` — see the MVP design spec's architecture decisions). The dashboard inherits that posture: no login, bound to localhost, for the same single-user reason.

## Scope

In scope:
- Browse listings with filters (search, state, city, price range, status).
- Create/edit/delete searches; create/edit regions; link regions to a search; enable/disable a search.
- View job/worker/source-session health; resume a paused source session; see aggregate stats.

Out of scope (explicitly deferred):
- Any authentication/authorization — matches the rest of the stack's current single-user posture. Revisit only if this ever stops being a single-user tool.
- Automated tests for the dashboard (no Jest/Playwright setup) — verified manually against the running Docker stack, the same way the crawler and backend's Docker smoke tests were verified in earlier plans. Go backend handlers already have their own test coverage; this UI is a thin client over them.
- Real-time updates (websockets/polling) — pages reflect data as of the last page load/navigation; a manual refresh is enough at this scale (a few jobs per hour, one worker).
- Editing/deleting listings — `listings` is a read path only; writes to it happen exclusively through the crawler's ingest pipeline (plan 1/3), never from a human via this UI.

## Architecture

**Stack:** Next.js 15 (App Router), TypeScript, Tailwind CSS (no component library — plain utility classes, per the "simple/functional admin panel" style decision). New service in `compose.yaml`: `dashboard`, built from a new `frontend/` directory (sibling to `backend/`/`crawler/`), port `3000`, depends on `api`.

**Data flow:** All API calls — reads and writes — happen server-side inside the Next.js container, talking to `http://api:8080` over the Docker-internal network (an `API_BASE_URL` env var, same pattern as the crawler's `API_BASE_URL`). The browser never calls the Go API directly. This means:
- Pages are React Server Components that `fetch(...)` the relevant `/api/v1/...` endpoint directly in the component body, with `{ cache: "no-store" }` (data changes every scheduler tick; stale cache would mislead).
- Mutations (create/edit a search or region, resume a session) are Next.js Server Actions — a `"use server"` function that does the `POST`/`PATCH` fetch, checks the response status, and on success calls `revalidatePath` for the affected page so the next render shows fresh data.
- No CORS configuration needed anywhere, and no change to the already-shipped Go backend.

**Navigation:** A top nav bar with 3 tabs — **Anúncios** (`/`), **Buscas** (`/searches`), **Saúde** (`/health`) — chosen over a sidebar for more horizontal room for wide tables/cards.

## Pages

### Anúncios (`/`)

Default/home page. A filter bar (search dropdown populated from `GET /api/v1/searches`, city/state text inputs, min/max price, status dropdown) that becomes query params on the page's own URL (e.g. `/?state=PR&max_price=3000000`), read server-side and forwarded as query params to `GET /api/v1/listings`. Results render as a card grid (photo thumbnail via `image_url`, title, formatted price, city/state, a relative "visto há X" from `last_seen_at`) — chosen over a dense table because recognizing "is this the car I want" from a thumbnail is the primary use case. A card without an `image_url` shows a placeholder icon, never a broken `<img>`. Clicking a card goes to `/listings/{id}`, which also renders the `snapshots` array (price history, since `GET /api/v1/listings/{id}` already returns both) as a simple reverse-chronological list.

### Buscas (`/searches`)

A table of existing searches (name, keyword, enabled toggle, frequency, linked regions) from `GET /api/v1/searches`, each row with "editar"/"excluir" actions. A "nova busca" button opens a form (name, keyword, frequency in minutes — converted to `frequency_seconds` before the API call per the backend's documented unit — enabled checkbox, multi-select of existing regions by id). Editing reuses the same form, pre-filled, submitting a `PATCH` instead of a `POST`. A region management section on the same page (simpler than splitting into a 4th nav tab, since regions are few and only exist to be attached to searches): a small table of regions from `GET /api/v1/regions` plus a "nova região" form (id, name, kind, city/state, lat/lng, radius, `fb_location`).

### Saúde (`/health`)

Three stacked sections on one page, each its own Server Component fetching independently: **Jobs** (table from `GET /api/v1/jobs`, status/error_kind filter dropdowns as query params same pattern as Anúncios), **Workers** (table from `GET /api/v1/workers`), **Sessões** (table from `GET /api/v1/sessions`, each row with a "retomar" button — a Server Action calling `POST /api/v1/sessions/{source}/resume` — enabled only when a session's status isn't already `ok`). A small stats strip at the top of the page from `GET /api/v1/stats` (total listings, enabled searches, jobs in the last 24h by status).

## Error handling

Every server-side `fetch` checks `response.ok` before parsing JSON — a non-2xx response renders an inline error message in the page/form rather than letting an unhandled exception produce Next.js's generic error boundary. Server Actions return a typed `{ ok: boolean, error?: string }` result that the calling form component uses to show a message next to the submit button, without a full page reload. Validation errors from the Go API (400, with a clean message per plan 3's `catalog.ErrValidation` handling) are shown verbatim, since the backend already guarantees those are human-readable and never leak internal errors — anything else (500) shows a generic "algo deu errado, tenta de novo" without the raw error, mirroring the backend's own public-facing error policy.

## Deployment

`compose.yaml` gains:
```yaml
  dashboard:
    build: ./frontend
    environment:
      API_BASE_URL: http://api:8080
    ports:
      - "127.0.0.1:3000:3000"
    depends_on:
      - api
    restart: unless-stopped
```
`frontend/Dockerfile` follows the same multi-stage pattern as `backend/Dockerfile` (build stage with the full toolchain, slim runtime stage) adapted for a Next.js production build (`next build` + `next start`, not a static export, since Server Components/Actions need a running Node process).
