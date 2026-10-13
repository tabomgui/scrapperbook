# Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Next.js dashboard (3 pages: Anúncios, Buscas, Saúde) over the existing `/api/v1`, so the user manages searches/regions and browses listings/health without `curl`/`psql`.

**Architecture:** Next.js 15 App Router, all data access server-side (React Server Components for reads, Server Actions for writes) talking to `http://api:8080` inside the Docker network — no CORS, no change to the Go backend. New `frontend/` directory, new `dashboard` service in `compose.yaml`.

**Tech Stack:** Next.js 15, React 19, TypeScript, Tailwind CSS 3 (no component library). No automated test suite for this plan — per the design spec, this UI is a thin client over an already-tested Go API, verified manually against the real running Docker stack (same approach used for the crawler's own smoke tests in earlier plans), not with Jest/Playwright.

**Spec:** `docs/superpowers/specs/2026-10-13-dashboard-design.md`

**Note on task structure:** Because there's no test runner here, each task's steps are "write the file(s)" → "verify it builds/behaves" → "commit", not the usual red/green TDD cycle. Verification tries `cd frontend && npm run build` first (fast, catches TypeScript/Next.js errors); if `node`/`npm` isn't installed on the host, fall back to `docker compose build dashboard` from the repo root (slower, but needs nothing but Docker, same as every other service in this repo).

---

## File structure

```text
frontend/
├── package.json
├── tsconfig.json
├── next.config.ts
├── tailwind.config.ts
├── postcss.config.mjs
├── Dockerfile
├── app/
│   ├── globals.css
│   ├── layout.tsx                  root layout: header + NavTabs
│   ├── error.tsx                   generic error boundary (no blank screen)
│   ├── not-found.tsx                generic 404 page
│   ├── page.tsx                     Anúncios (listings, filters, card grid)
│   ├── listings/[id]/page.tsx       listing detail + price history
│   ├── searches/
│   │   ├── page.tsx                 searches + regions tables
│   │   ├── actions.ts                Server Actions: create/update/delete search, create/update region
│   │   ├── new/page.tsx              new search form
│   │   ├── [id]/edit/page.tsx        edit search form
│   │   └── regions/
│   │       ├── new/page.tsx          new region form
│   │       └── [id]/edit/page.tsx    edit region form
│   └── health/
│       ├── page.tsx                  stats + sessions + workers + jobs
│       └── actions.ts                Server Action: resume session
├── components/
│   ├── NavTabs.tsx                   client component, top nav with active tab
│   ├── SearchForm.tsx                 client component, shared create/edit form
│   └── RegionForm.tsx                 client component, shared create/edit form
└── lib/
    ├── api.ts                        fetch wrapper, error handling, null-list coalescing
    ├── types.ts                       TS interfaces mirroring the Go API's JSON shapes
    └── format.ts                      formatPrice/timeAgo helpers
```

`compose.yaml` gains a 4th service, `dashboard`, alongside `postgres`/`api`/`crawler`.

Prerequisite: Docker running (same as every other plan in this project). Node.js is NOT required on the host — only inside the container — but having it locally makes iteration faster (see note above).

---

### Task 1: Scaffold the Next.js app, shared API client, layout/nav, and Docker wiring

**Files:**
- Create: `frontend/package.json`
- Create: `frontend/tsconfig.json`
- Create: `frontend/next.config.ts`
- Create: `frontend/tailwind.config.ts`
- Create: `frontend/postcss.config.mjs`
- Create: `frontend/Dockerfile`
- Create: `frontend/app/globals.css`
- Create: `frontend/app/layout.tsx`
- Create: `frontend/app/error.tsx`
- Create: `frontend/app/not-found.tsx`
- Create: `frontend/app/page.tsx`
- Create: `frontend/components/NavTabs.tsx`
- Create: `frontend/lib/api.ts`
- Create: `frontend/lib/types.ts`
- Create: `frontend/lib/format.ts`
- Modify: `compose.yaml`

- [ ] **Step 1: Criar `package.json`**

`frontend/package.json`:

```json
{
  "name": "scrapperbook-dashboard",
  "version": "0.1.0",
  "private": true,
  "scripts": {
    "dev": "next dev",
    "build": "next build",
    "start": "next start",
    "lint": "next lint"
  },
  "dependencies": {
    "next": "^15.0.0",
    "react": "^19.0.0",
    "react-dom": "^19.0.0"
  },
  "devDependencies": {
    "@types/node": "^22.0.0",
    "@types/react": "^19.0.0",
    "@types/react-dom": "^19.0.0",
    "typescript": "^5.6.0",
    "tailwindcss": "^3.4.0",
    "postcss": "^8.4.0",
    "autoprefixer": "^10.4.0"
  }
}
```

- [ ] **Step 2: Criar configs**

`frontend/tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2017",
    "lib": ["dom", "dom.iterable", "esnext"],
    "allowJs": true,
    "skipLibCheck": true,
    "strict": true,
    "noEmit": true,
    "esModuleInterop": true,
    "module": "esnext",
    "moduleResolution": "bundler",
    "resolveJsonModule": true,
    "isolatedModules": true,
    "jsx": "preserve",
    "incremental": true,
    "plugins": [{ "name": "next" }],
    "paths": { "@/*": ["./*"] }
  },
  "include": ["next-env.d.ts", "**/*.ts", "**/*.tsx", ".next/types/**/*.ts"],
  "exclude": ["node_modules"]
}
```

`frontend/next.config.ts`:

```ts
import type { NextConfig } from "next";

const nextConfig: NextConfig = {};

export default nextConfig;
```

`frontend/tailwind.config.ts`:

```ts
import type { Config } from "tailwindcss";

const config: Config = {
  content: ["./app/**/*.{ts,tsx}", "./components/**/*.{ts,tsx}"],
  theme: { extend: {} },
  plugins: [],
};

export default config;
```

`frontend/postcss.config.mjs`:

```js
export default {
  plugins: {
    tailwindcss: {},
    autoprefixer: {},
  },
};
```

`frontend/app/globals.css`:

```css
@tailwind base;
@tailwind components;
@tailwind utilities;

body {
  background-color: #f8fafc;
  color: #0f172a;
}
```

- [ ] **Step 3: Criar o cliente de API (`lib/api.ts`)**

This wraps every call to the Go backend: sets the base URL from `API_BASE_URL` (set in `compose.yaml`, defaults to `localhost:8080` for local `npm run dev` outside Docker), disables caching (data changes every scheduler tick), throws a typed `ApiError` with the backend's own clean message on a non-2xx response (per the design spec's error-handling section), and coalesces a `null` JSON body (Go serializes a nil/empty slice as `null`, not `[]`) to an empty array for list endpoints.

`frontend/lib/api.ts`:

```ts
const API_BASE_URL = process.env.API_BASE_URL ?? "http://localhost:8080";

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE_URL}${path}`, {
    ...init,
    cache: "no-store",
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  if (!res.ok) {
    let message = `request failed with status ${res.status}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body?.error) message = body.error;
    } catch {
      // response had no JSON body; keep the generic message
    }
    throw new ApiError(res.status, message);
  }
  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}

export function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  return request<T>(path, init);
}

export async function apiFetchList<T>(path: string, init?: RequestInit): Promise<T[]> {
  const result = await request<T[] | null>(path, init);
  return result ?? [];
}
```

- [ ] **Step 4: Criar os tipos (`lib/types.ts`)**

These mirror the exact JSON shapes `backend/internal/catalog`, `backend/internal/workers`, `backend/internal/jobs`, and `backend/internal/httpapi`'s `statsResponse` produce — field names and optionality (Go's `,omitempty` on a nil pointer means the key is absent, i.e. `undefined` in TS, not `null`).

`frontend/lib/types.ts`:

```ts
export interface Region {
  id: string;
  name: string;
  kind: string;
  city?: string;
  state?: string;
  lat?: number;
  lng?: number;
  radius_km?: number;
  fb_location?: string;
  enabled: boolean;
}

export interface Search {
  id: string;
  name: string;
  keyword: string;
  enabled: boolean;
  priority: number;
  frequency_seconds: number;
  filters: unknown;
  alert: unknown;
  region_ids: string[];
}

export interface Listing {
  id: number;
  source: string;
  external_id: string;
  url: string;
  title: string;
  description?: string;
  price_cents?: number;
  currency?: string;
  city?: string;
  state?: string;
  image_url?: string;
  seller_name?: string;
  status: string;
  first_seen_at: string;
  last_seen_at: string;
}

export interface Snapshot {
  price_cents?: number;
  title: string;
  status: string;
  captured_at: string;
}

export interface JobSummary {
  id: string;
  type: string;
  source: string;
  search_id?: string;
  region_id?: string;
  status: string;
  attempts: number;
  error_kind?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
}

export interface Worker {
  id: string;
  hostname: string;
  status: string;
  crawler_version: string;
  sources: string[] | null;
  current_job_id?: string;
  jobs_ok: number;
  jobs_failed: number;
  cooldown_until?: string;
  registered_at: string;
  last_heartbeat_at: string;
}

export interface SourceSession {
  source: string;
  status: string;
  last_checkpoint_at?: string;
  cooldown_until?: string;
  note?: string;
}

export interface Stats {
  total_listings: number;
  listings_by_state: Record<string, number>;
  enabled_searches: number;
  jobs_last_24h: Record<string, number>;
  workers_by_status: Record<string, number>;
}
```

- [ ] **Step 5: Criar os helpers de formatação (`lib/format.ts`)**

`frontend/lib/format.ts`:

```ts
export function formatPrice(cents?: number): string {
  if (cents == null) return "sem preço";
  return `R$ ${(cents / 100).toLocaleString("pt-BR", { minimumFractionDigits: 2 })}`;
}

export function timeAgo(iso: string): string {
  const diffMs = Date.now() - new Date(iso).getTime();
  const hours = Math.floor(diffMs / 3_600_000);
  if (hours < 1) return "visto agora";
  if (hours < 24) return `visto há ${hours}h`;
  return `visto há ${Math.floor(hours / 24)}d`;
}
```

- [ ] **Step 6: Criar o layout raiz, a navegação e as páginas de erro**

`frontend/components/NavTabs.tsx`:

```tsx
"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

const TABS = [
  { href: "/", label: "Anúncios" },
  { href: "/searches", label: "Buscas" },
  { href: "/health", label: "Saúde" },
];

export function NavTabs() {
  const pathname = usePathname();
  return (
    <nav className="flex gap-4 text-sm">
      {TABS.map((tab) => {
        const active = tab.href === "/" ? pathname === "/" : pathname.startsWith(tab.href);
        return (
          <Link
            key={tab.href}
            href={tab.href}
            className={
              active
                ? "border-b-2 border-slate-900 pb-2 font-medium text-slate-900"
                : "border-b-2 border-transparent pb-2 text-slate-500 hover:text-slate-900"
            }
          >
            {tab.label}
          </Link>
        );
      })}
    </nav>
  );
}
```

`frontend/app/layout.tsx`:

```tsx
import type { Metadata } from "next";
import "./globals.css";
import { NavTabs } from "@/components/NavTabs";

export const metadata: Metadata = {
  title: "Marketplace Monitor",
  description: "Dashboard do marketplace monitor",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="pt-BR">
      <body>
        <header className="border-b border-slate-200 bg-white">
          <div className="mx-auto max-w-6xl px-4 py-3">
            <div className="mb-2 text-lg font-semibold">Marketplace Monitor</div>
            <NavTabs />
          </div>
        </header>
        <main className="mx-auto max-w-6xl px-4 py-6">{children}</main>
      </body>
    </html>
  );
}
```

`frontend/app/error.tsx`:

```tsx
"use client";

export default function Error({ error, reset }: { error: Error; reset: () => void }) {
  return (
    <div className="rounded border border-red-200 bg-red-50 p-4 text-red-800">
      <p className="font-medium">Algo deu errado.</p>
      <p className="text-sm">{error.message}</p>
      <button onClick={reset} className="mt-2 rounded bg-red-800 px-3 py-1 text-sm text-white">
        Tentar de novo
      </button>
    </div>
  );
}
```

`frontend/app/not-found.tsx`:

```tsx
export default function NotFound() {
  return (
    <div className="rounded border border-slate-200 bg-white p-4">
      <p className="font-medium">Não encontrado.</p>
      <a href="/" className="text-sm text-slate-500 hover:text-slate-900">
        ← voltar
      </a>
    </div>
  );
}
```

- [ ] **Step 7: Criar uma página inicial mínima (ainda sem filtros/cards — isso é a Task 2)**

`frontend/app/page.tsx`:

```tsx
import { apiFetchList } from "@/lib/api";
import { formatPrice } from "@/lib/format";
import type { Listing } from "@/lib/types";

export default async function ListingsPage() {
  const listings = await apiFetchList<Listing>("/api/v1/listings");
  return (
    <div>
      <h1 className="mb-4 text-xl font-semibold">Anúncios</h1>
      {listings.length === 0 && <p className="text-slate-500">Nenhum anúncio ainda.</p>}
      <ul className="space-y-2">
        {listings.map((l) => (
          <li key={l.id} className="rounded border border-slate-200 bg-white p-3">
            {l.title} — {formatPrice(l.price_cents)}
          </li>
        ))}
      </ul>
    </div>
  );
}
```

- [ ] **Step 8: Criar `public/.gitkeep`**

Next.js doesn't require a `public/` directory to exist, but the Dockerfile below does a `COPY --from=build /app/public ./public`, which fails the build if the directory doesn't exist at all. Create an empty one now so that line always has something to copy, even before any real static asset is ever added there:

```bash
mkdir -p frontend/public && touch frontend/public/.gitkeep
```

- [ ] **Step 9: Criar o Dockerfile**

`frontend/Dockerfile`:

```dockerfile
FROM node:22-alpine AS build
WORKDIR /app
COPY package.json ./
RUN npm install
COPY . .
RUN npm run build

FROM node:22-alpine AS runtime
WORKDIR /app
ENV NODE_ENV=production
COPY --from=build /app/public ./public
COPY --from=build /app/.next ./.next
COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/package.json ./package.json
EXPOSE 3000
CMD ["npm", "run", "start"]
```

(No `public/` directory exists yet — Next.js doesn't require one; if `npm run build` complains about the missing `COPY --from=build /app/public ./public` line, create an empty `frontend/public/.gitkeep` file rather than removing the line, so the Dockerfile doesn't need to change later if static assets are added.)

- [ ] **Step 10: Adicionar o serviço `dashboard` ao `compose.yaml`**

Read the current `compose.yaml` first. Add this new service (after `crawler`, before the `volumes:` key):

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

- [ ] **Step 11: Verificar**

If `node`/`npm` is available on the host:
```bash
cd frontend && npm install && npm run build
```
Expected: build succeeds, no TypeScript errors (fix any that come up — this is new code in this repo, there's no existing pattern to match yet, so use idiomatic Next.js/TypeScript).

Either way, verify the Docker path works (this is what actually matters, since this is how it runs in this project):
```bash
docker compose build dashboard
docker compose up -d dashboard
curl -s http://localhost:3000/ | grep -o "Anúncios"
```
Expected: `docker compose build` succeeds; the curl output includes `Anúncios` (confirming the page rendered server-side with real data, not an error page).

- [ ] **Step 12: Commit** — SKIP if working in a git-less execution directory (check first: `git status` — if it fails with "not a git repository", there is nothing to commit here; this is expected for this project's established workflow, where the execution directory has no git and the plan document's own repo is committed separately). If a git repo DOES exist in the working directory, run:

```bash
git add frontend/ compose.yaml
git commit -m "feat(dashboard): scaffold Next.js app, API client, layout and nav"
```

---

### Task 2: Anúncios — filters, card grid, listing detail page

**Files:**
- Modify: `frontend/app/page.tsx` (replace Task 1's minimal version)
- Create: `frontend/app/listings/[id]/page.tsx`

- [ ] **Step 1: Substituir `app/page.tsx` pela versão completa**

`frontend/app/page.tsx`:

```tsx
import Link from "next/link";
import { apiFetchList } from "@/lib/api";
import { formatPrice, timeAgo } from "@/lib/format";
import type { Listing, Search } from "@/lib/types";

type SearchParams = {
  search_id?: string;
  state?: string;
  city?: string;
  min_price_reais?: string;
  max_price_reais?: string;
  status?: string;
};

export default async function ListingsPage({
  searchParams,
}: {
  searchParams: Promise<SearchParams>;
}) {
  const params = await searchParams;
  const query = new URLSearchParams();
  if (params.search_id) query.set("search_id", params.search_id);
  if (params.state) query.set("state", params.state);
  if (params.city) query.set("city", params.city);
  if (params.min_price_reais) {
    query.set("min_price", String(Math.round(Number(params.min_price_reais) * 100)));
  }
  if (params.max_price_reais) {
    query.set("max_price", String(Math.round(Number(params.max_price_reais) * 100)));
  }
  if (params.status) query.set("status", params.status);

  const [listings, searches] = await Promise.all([
    apiFetchList<Listing>(`/api/v1/listings?${query.toString()}`),
    apiFetchList<Search>("/api/v1/searches"),
  ]);

  return (
    <div>
      <h1 className="mb-4 text-xl font-semibold">Anúncios</h1>
      <form className="mb-6 flex flex-wrap gap-3 rounded border border-slate-200 bg-white p-4 text-sm">
        <select
          name="search_id"
          defaultValue={params.search_id ?? ""}
          className="rounded border border-slate-300 px-2 py-1"
        >
          <option value="">Todas as buscas</option>
          {searches.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
        <input
          name="state"
          placeholder="Estado (ex: PR)"
          defaultValue={params.state ?? ""}
          className="w-28 rounded border border-slate-300 px-2 py-1"
        />
        <input
          name="city"
          placeholder="Cidade"
          defaultValue={params.city ?? ""}
          className="w-36 rounded border border-slate-300 px-2 py-1"
        />
        <input
          name="min_price_reais"
          type="number"
          placeholder="Preço mín (R$)"
          defaultValue={params.min_price_reais ?? ""}
          className="w-32 rounded border border-slate-300 px-2 py-1"
        />
        <input
          name="max_price_reais"
          type="number"
          placeholder="Preço máx (R$)"
          defaultValue={params.max_price_reais ?? ""}
          className="w-32 rounded border border-slate-300 px-2 py-1"
        />
        <select name="status" defaultValue={params.status ?? ""} className="rounded border border-slate-300 px-2 py-1">
          <option value="">Todos os status</option>
          <option value="active">Ativo</option>
          <option value="unseen">Sumiu</option>
        </select>
        <button type="submit" className="rounded bg-slate-900 px-3 py-1 font-medium text-white">
          Filtrar
        </button>
      </form>

      {listings.length === 0 && <p className="text-slate-500">Nenhum anúncio encontrado com esses filtros.</p>}

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {listings.map((l) => (
          <Link
            key={l.id}
            href={`/listings/${l.id}`}
            className="rounded border border-slate-200 bg-white p-3 hover:border-slate-400"
          >
            <div className="mb-2 flex h-32 items-center justify-center overflow-hidden rounded bg-slate-100">
              {l.image_url ? (
                // eslint-disable-next-line @next/next/no-img-element -- arbitrary external CDN hosts, next/image would need an allowlist we can't predict
                <img src={l.image_url} alt={l.title} className="h-full w-full object-cover" />
              ) : (
                <span className="text-3xl">🚗</span>
              )}
            </div>
            <div className="text-sm font-medium">{l.title}</div>
            <div className="text-xs text-slate-500">
              {formatPrice(l.price_cents)} · {l.city ?? "?"}/{l.state ?? "?"}
            </div>
            <div className="text-xs text-slate-400">{timeAgo(l.last_seen_at)}</div>
          </Link>
        ))}
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Criar a página de detalhe**

`frontend/app/listings/[id]/page.tsx`:

```tsx
import { notFound } from "next/navigation";
import { apiFetch, ApiError } from "@/lib/api";
import { formatPrice } from "@/lib/format";
import type { Listing, Snapshot } from "@/lib/types";

export default async function ListingDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  let data: { listing: Listing; snapshots: Snapshot[] | null };
  try {
    data = await apiFetch<{ listing: Listing; snapshots: Snapshot[] | null }>(`/api/v1/listings/${id}`);
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) notFound();
    throw err;
  }
  const snapshots = data.snapshots ?? [];

  return (
    <div>
      <a href="/" className="text-sm text-slate-500 hover:text-slate-900">
        ← voltar
      </a>
      <h1 className="mt-2 mb-1 text-xl font-semibold">{data.listing.title}</h1>
      <p className="mb-4 text-slate-600">
        {formatPrice(data.listing.price_cents)} · {data.listing.city ?? "?"}/{data.listing.state ?? "?"} · status:{" "}
        {data.listing.status}
      </p>
      {data.listing.image_url && (
        // eslint-disable-next-line @next/next/no-img-element -- arbitrary external CDN hosts
        <img
          src={data.listing.image_url}
          alt={data.listing.title}
          className="mb-4 max-h-80 rounded border border-slate-200 object-cover"
        />
      )}
      <a
        href={data.listing.url}
        target="_blank"
        rel="noreferrer"
        className="mb-6 inline-block rounded bg-slate-900 px-3 py-1 text-sm font-medium text-white"
      >
        Ver no Facebook Marketplace
      </a>

      <h2 className="mb-2 text-lg font-medium">Histórico</h2>
      {snapshots.length === 0 ? (
        <p className="text-slate-500">Sem histórico ainda.</p>
      ) : (
        <ul className="space-y-1 text-sm">
          {[...snapshots].reverse().map((s, i) => (
            <li key={i} className="rounded border border-slate-200 bg-white p-2">
              {new Date(s.captured_at).toLocaleString("pt-BR")} — {formatPrice(s.price_cents)} — {s.status}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
```

- [ ] **Step 3: Verificar**

```bash
cd frontend && npm run build
```
(or `docker compose build dashboard` from the repo root if `npm` isn't available locally — see Task 1's note)

Expected: build succeeds. Then:
```bash
docker compose up -d --build dashboard
curl -s "http://localhost:3000/?state=PR" | grep -o "Filtrar"
```
Expected: page renders with the filter form present.

- [ ] **Step 4: Commit** — same rule as Task 1 Step 12 (skip if no git repo in the working directory).

```bash
git add frontend/app/page.tsx frontend/app/listings/
git commit -m "feat(dashboard): add listing filters, card grid and detail page"
```

---

### Task 3: Buscas — searches and regions, create/edit forms

**Files:**
- Create: `frontend/app/searches/actions.ts`
- Create: `frontend/app/searches/page.tsx`
- Create: `frontend/app/searches/new/page.tsx`
- Create: `frontend/app/searches/[id]/edit/page.tsx`
- Create: `frontend/app/searches/regions/new/page.tsx`
- Create: `frontend/app/searches/regions/[id]/edit/page.tsx`
- Create: `frontend/components/SearchForm.tsx`
- Create: `frontend/components/RegionForm.tsx`

Note: the Go API has no `GET /api/v1/regions/{id}` (only list/create/update) — the region edit page below fetches the full list and finds the one it needs, same data the regions table already fetches.

- [ ] **Step 1: Criar as Server Actions**

`frontend/app/searches/actions.ts`:

```ts
"use server";

import { revalidatePath } from "next/cache";
import { redirect } from "next/navigation";
import { apiFetch, ApiError } from "@/lib/api";

export type ActionResult = { ok: true } | { ok: false; error: string };

function parseFrequencyMinutes(raw: FormDataEntryValue | null): number {
  return Math.round(Number(raw) * 60);
}

export async function createSearchAction(formData: FormData): Promise<ActionResult> {
  const body = {
    name: String(formData.get("name") ?? ""),
    keyword: String(formData.get("keyword") ?? ""),
    enabled: formData.get("enabled") === "on",
    priority: Number(formData.get("priority") ?? 0),
    frequency_seconds: parseFrequencyMinutes(formData.get("frequency_minutes")),
    region_ids: formData.getAll("region_ids").map(String),
  };
  try {
    await apiFetch("/api/v1/searches", { method: "POST", body: JSON.stringify(body) });
  } catch (err) {
    return { ok: false, error: err instanceof ApiError ? err.message : "erro inesperado" };
  }
  revalidatePath("/searches");
  redirect("/searches");
}

export async function updateSearchAction(id: string, formData: FormData): Promise<ActionResult> {
  const body = {
    name: String(formData.get("name") ?? ""),
    keyword: String(formData.get("keyword") ?? ""),
    enabled: formData.get("enabled") === "on",
    priority: Number(formData.get("priority") ?? 0),
    frequency_seconds: parseFrequencyMinutes(formData.get("frequency_minutes")),
    region_ids: formData.getAll("region_ids").map(String),
  };
  try {
    await apiFetch(`/api/v1/searches/${id}`, { method: "PATCH", body: JSON.stringify(body) });
  } catch (err) {
    return { ok: false, error: err instanceof ApiError ? err.message : "erro inesperado" };
  }
  revalidatePath("/searches");
  redirect("/searches");
}

export async function deleteSearchAction(id: string): Promise<void> {
  await apiFetch(`/api/v1/searches/${id}`, { method: "DELETE" });
  revalidatePath("/searches");
}

export async function createRegionAction(formData: FormData): Promise<ActionResult> {
  const body = {
    id: String(formData.get("id") ?? ""),
    name: String(formData.get("name") ?? ""),
    kind: String(formData.get("kind") ?? "city"),
    city: String(formData.get("city") ?? "") || undefined,
    state: String(formData.get("state") ?? "") || undefined,
    fb_location: String(formData.get("fb_location") ?? "") || undefined,
    enabled: formData.get("enabled") === "on",
  };
  try {
    await apiFetch("/api/v1/regions", { method: "POST", body: JSON.stringify(body) });
  } catch (err) {
    return { ok: false, error: err instanceof ApiError ? err.message : "erro inesperado" };
  }
  revalidatePath("/searches");
  redirect("/searches");
}

export async function updateRegionAction(id: string, formData: FormData): Promise<ActionResult> {
  const body = {
    name: String(formData.get("name") ?? ""),
    city: String(formData.get("city") ?? "") || undefined,
    state: String(formData.get("state") ?? "") || undefined,
    fb_location: String(formData.get("fb_location") ?? "") || undefined,
    enabled: formData.get("enabled") === "on",
  };
  try {
    await apiFetch(`/api/v1/regions/${id}`, { method: "PATCH", body: JSON.stringify(body) });
  } catch (err) {
    return { ok: false, error: err instanceof ApiError ? err.message : "erro inesperado" };
  }
  revalidatePath("/searches");
  redirect("/searches");
}
```

- [ ] **Step 2: Criar os formulários client-side compartilhados**

`frontend/components/SearchForm.tsx`:

```tsx
"use client";

import { useActionState } from "react";
import type { Region, Search } from "@/lib/types";
import type { ActionResult } from "@/app/searches/actions";

type Props = {
  action: (formData: FormData) => Promise<ActionResult>;
  regions: Region[];
  initial?: Search;
};

const initialState: ActionResult = { ok: true };

export function SearchForm({ action, regions, initial }: Props) {
  const [state, formAction, isPending] = useActionState(
    async (_prev: ActionResult, formData: FormData) => action(formData),
    initialState,
  );

  return (
    <form action={formAction} className="max-w-md space-y-3">
      <div>
        <label className="block text-sm font-medium">Nome</label>
        <input
          name="name"
          defaultValue={initial?.name}
          required
          className="mt-1 w-full rounded border border-slate-300 px-2 py-1"
        />
      </div>
      <div>
        <label className="block text-sm font-medium">Palavra-chave</label>
        <input
          name="keyword"
          defaultValue={initial?.keyword}
          required
          className="mt-1 w-full rounded border border-slate-300 px-2 py-1"
        />
      </div>
      <div>
        <label className="block text-sm font-medium">Frequência (minutos)</label>
        <input
          name="frequency_minutes"
          type="number"
          min={1}
          defaultValue={initial ? Math.round(initial.frequency_seconds / 60) : 30}
          required
          className="mt-1 w-full rounded border border-slate-300 px-2 py-1"
        />
      </div>
      <div>
        <label className="block text-sm font-medium">Prioridade</label>
        <input
          name="priority"
          type="number"
          defaultValue={initial?.priority ?? 0}
          className="mt-1 w-full rounded border border-slate-300 px-2 py-1"
        />
      </div>
      <div>
        <label className="block text-sm font-medium">Regiões</label>
        <select
          name="region_ids"
          multiple
          defaultValue={initial?.region_ids}
          className="mt-1 w-full rounded border border-slate-300 px-2 py-1"
        >
          {regions.map((r) => (
            <option key={r.id} value={r.id}>
              {r.name}
            </option>
          ))}
        </select>
      </div>
      <div className="flex items-center gap-2">
        <input type="checkbox" name="enabled" id="enabled" defaultChecked={initial?.enabled ?? true} />
        <label htmlFor="enabled" className="text-sm">
          Ativa
        </label>
      </div>
      {!state.ok && <p className="text-sm text-red-600">{state.error}</p>}
      <button
        type="submit"
        disabled={isPending}
        className="rounded bg-slate-900 px-3 py-1 text-sm font-medium text-white disabled:opacity-50"
      >
        {isPending ? "Salvando..." : "Salvar"}
      </button>
    </form>
  );
}
```

`frontend/components/RegionForm.tsx`:

```tsx
"use client";

import { useActionState } from "react";
import type { Region } from "@/lib/types";
import type { ActionResult } from "@/app/searches/actions";

type Props = {
  action: (formData: FormData) => Promise<ActionResult>;
  initial?: Region;
};

const initialState: ActionResult = { ok: true };

export function RegionForm({ action, initial }: Props) {
  const [state, formAction, isPending] = useActionState(
    async (_prev: ActionResult, formData: FormData) => action(formData),
    initialState,
  );

  return (
    <form action={formAction} className="max-w-md space-y-3">
      {!initial && (
        <div>
          <label className="block text-sm font-medium">ID (identificador único, ex: curitiba-pr)</label>
          <input name="id" required className="mt-1 w-full rounded border border-slate-300 px-2 py-1" />
        </div>
      )}
      <div>
        <label className="block text-sm font-medium">Nome</label>
        <input
          name="name"
          defaultValue={initial?.name}
          required
          className="mt-1 w-full rounded border border-slate-300 px-2 py-1"
        />
      </div>
      <div>
        <label className="block text-sm font-medium">Tipo</label>
        <select name="kind" defaultValue={initial?.kind ?? "city"} className="mt-1 w-full rounded border border-slate-300 px-2 py-1">
          <option value="city">Cidade</option>
          <option value="point">Ponto</option>
          <option value="state">Estado</option>
        </select>
      </div>
      <div>
        <label className="block text-sm font-medium">Cidade</label>
        <input name="city" defaultValue={initial?.city} className="mt-1 w-full rounded border border-slate-300 px-2 py-1" />
      </div>
      <div>
        <label className="block text-sm font-medium">Estado (UF)</label>
        <input name="state" defaultValue={initial?.state} className="mt-1 w-full rounded border border-slate-300 px-2 py-1" />
      </div>
      <div>
        <label className="block text-sm font-medium">fb_location (slug do Facebook)</label>
        <input
          name="fb_location"
          defaultValue={initial?.fb_location}
          className="mt-1 w-full rounded border border-slate-300 px-2 py-1"
        />
      </div>
      <div className="flex items-center gap-2">
        <input type="checkbox" name="enabled" id="enabled" defaultChecked={initial?.enabled ?? true} />
        <label htmlFor="enabled" className="text-sm">
          Ativa
        </label>
      </div>
      {!state.ok && <p className="text-sm text-red-600">{state.error}</p>}
      <button
        type="submit"
        disabled={isPending}
        className="rounded bg-slate-900 px-3 py-1 text-sm font-medium text-white disabled:opacity-50"
      >
        {isPending ? "Salvando..." : "Salvar"}
      </button>
    </form>
  );
}
```

- [ ] **Step 3: Criar a página de listagem**

`frontend/app/searches/page.tsx`:

```tsx
import Link from "next/link";
import { apiFetchList } from "@/lib/api";
import type { Region, Search } from "@/lib/types";
import { deleteSearchAction } from "./actions";

export default async function SearchesPage() {
  const [searches, regions] = await Promise.all([
    apiFetchList<Search>("/api/v1/searches"),
    apiFetchList<Region>("/api/v1/regions"),
  ]);
  const regionNameById = new Map(regions.map((r) => [r.id, r.name]));

  return (
    <div className="space-y-10">
      <section>
        <div className="mb-3 flex items-center justify-between">
          <h1 className="text-xl font-semibold">Buscas</h1>
          <Link href="/searches/new" className="rounded bg-slate-900 px-3 py-1 text-sm font-medium text-white">
            + nova busca
          </Link>
        </div>
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-slate-200 text-left text-slate-500">
              <th className="py-2">Nome</th>
              <th>Palavra-chave</th>
              <th>Ativa</th>
              <th>Frequência</th>
              <th>Regiões</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {searches.map((s) => (
              <tr key={s.id} className="border-b border-slate-100">
                <td className="py-2">{s.name}</td>
                <td>{s.keyword}</td>
                <td>{s.enabled ? "sim" : "não"}</td>
                <td>{Math.round(s.frequency_seconds / 60)} min</td>
                <td>{s.region_ids.map((id) => regionNameById.get(id) ?? id).join(", ") || "—"}</td>
                <td className="space-x-2 text-right">
                  <Link href={`/searches/${s.id}/edit`} className="text-slate-600 hover:text-slate-900">
                    editar
                  </Link>
                  <form action={deleteSearchAction.bind(null, s.id)} className="inline">
                    <button type="submit" className="text-red-600 hover:text-red-800">
                      excluir
                    </button>
                  </form>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {searches.length === 0 && <p className="mt-2 text-slate-500">Nenhuma busca cadastrada ainda.</p>}
      </section>

      <section>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-lg font-medium">Regiões</h2>
          <Link
            href="/searches/regions/new"
            className="rounded bg-slate-900 px-3 py-1 text-sm font-medium text-white"
          >
            + nova região
          </Link>
        </div>
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-slate-200 text-left text-slate-500">
              <th className="py-2">Nome</th>
              <th>Cidade/Estado</th>
              <th>fb_location</th>
              <th>Ativa</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {regions.map((r) => (
              <tr key={r.id} className="border-b border-slate-100">
                <td className="py-2">{r.name}</td>
                <td>
                  {r.city ?? "?"}/{r.state ?? "?"}
                </td>
                <td>{r.fb_location ?? "—"}</td>
                <td>{r.enabled ? "sim" : "não"}</td>
                <td className="text-right">
                  <Link href={`/searches/regions/${r.id}/edit`} className="text-slate-600 hover:text-slate-900">
                    editar
                  </Link>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {regions.length === 0 && <p className="mt-2 text-slate-500">Nenhuma região cadastrada ainda.</p>}
      </section>
    </div>
  );
}
```

- [ ] **Step 4: Criar as páginas de criação/edição**

`frontend/app/searches/new/page.tsx`:

```tsx
import { apiFetchList } from "@/lib/api";
import type { Region } from "@/lib/types";
import { SearchForm } from "@/components/SearchForm";
import { createSearchAction } from "../actions";

export default async function NewSearchPage() {
  const regions = await apiFetchList<Region>("/api/v1/regions");
  return (
    <div>
      <h1 className="mb-4 text-xl font-semibold">Nova busca</h1>
      <SearchForm action={createSearchAction} regions={regions} />
    </div>
  );
}
```

`frontend/app/searches/[id]/edit/page.tsx`:

```tsx
import { notFound } from "next/navigation";
import { apiFetch, apiFetchList, ApiError } from "@/lib/api";
import type { Region, Search } from "@/lib/types";
import { SearchForm } from "@/components/SearchForm";
import { updateSearchAction } from "../../actions";

export default async function EditSearchPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  let search: Search;
  try {
    search = await apiFetch<Search>(`/api/v1/searches/${id}`);
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) notFound();
    throw err;
  }
  const regions = await apiFetchList<Region>("/api/v1/regions");
  return (
    <div>
      <h1 className="mb-4 text-xl font-semibold">Editar busca</h1>
      <SearchForm action={updateSearchAction.bind(null, id)} regions={regions} initial={search} />
    </div>
  );
}
```

`frontend/app/searches/regions/new/page.tsx`:

```tsx
import { RegionForm } from "@/components/RegionForm";
import { createRegionAction } from "../../actions";

export default function NewRegionPage() {
  return (
    <div>
      <h1 className="mb-4 text-xl font-semibold">Nova região</h1>
      <RegionForm action={createRegionAction} />
    </div>
  );
}
```

`frontend/app/searches/regions/[id]/edit/page.tsx`:

```tsx
import { notFound } from "next/navigation";
import { apiFetchList } from "@/lib/api";
import type { Region } from "@/lib/types";
import { RegionForm } from "@/components/RegionForm";
import { updateRegionAction } from "../../../actions";

export default async function EditRegionPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const regions = await apiFetchList<Region>("/api/v1/regions");
  const region = regions.find((r) => r.id === id);
  if (!region) notFound();
  return (
    <div>
      <h1 className="mb-4 text-xl font-semibold">Editar região</h1>
      <RegionForm action={updateRegionAction.bind(null, id)} initial={region} />
    </div>
  );
}
```

- [ ] **Step 5: Verificar**

```bash
cd frontend && npm run build
```
(or `docker compose build dashboard` — see Task 1's note)

Expected: build succeeds.

```bash
docker compose up -d --build dashboard
curl -s http://localhost:3000/searches | grep -o "nova busca"
curl -s http://localhost:3000/searches/new | grep -o "Palavra-chave"
curl -s http://localhost:3000/searches/regions/new | grep -o "identificador único"
```
Expected: each page renders its form.

Then, manually in a browser: create a test region (e.g. `id=teste-sp`, `name=Teste SP`, `city=São Paulo`, `state=SP`, `fb_location=sao-paulo`), confirm it appears in the regions table on `/searches`; create a test search linking that region, confirm it appears in the searches table with the region name listed; click "editar" on it, change the keyword, save, confirm the change is reflected; click "excluir", confirm it's removed. Then delete the test region the same way if a delete-region path exists — if not (the API only has create/update for regions, no delete endpoint), leave it; note this in your report rather than inventing a delete button for an endpoint that doesn't exist.

- [ ] **Step 6: Commit** — same rule as Task 1 Step 12.

```bash
git add frontend/app/searches/ frontend/components/SearchForm.tsx frontend/components/RegionForm.tsx
git commit -m "feat(dashboard): add search and region management pages"
```

---

### Task 4: Saúde — stats, sessions, workers, jobs

**Files:**
- Create: `frontend/app/health/actions.ts`
- Create: `frontend/app/health/page.tsx`

- [ ] **Step 1: Criar a Server Action de resume**

`frontend/app/health/actions.ts`:

```ts
"use server";

import { revalidatePath } from "next/cache";
import { apiFetch } from "@/lib/api";

export async function resumeSessionAction(source: string): Promise<void> {
  await apiFetch(`/api/v1/sessions/${source}/resume`, { method: "POST" });
  revalidatePath("/health");
}
```

- [ ] **Step 2: Criar a página**

`frontend/app/health/page.tsx`:

```tsx
import { apiFetch, apiFetchList } from "@/lib/api";
import type { JobSummary, SourceSession, Stats, Worker } from "@/lib/types";
import { resumeSessionAction } from "./actions";

export default async function HealthPage({
  searchParams,
}: {
  searchParams: Promise<{ status?: string; error_kind?: string }>;
}) {
  const params = await searchParams;
  const jobQuery = new URLSearchParams();
  if (params.status) jobQuery.set("status", params.status);
  if (params.error_kind) jobQuery.set("error_kind", params.error_kind);

  const [stats, jobs, workers, sessions] = await Promise.all([
    apiFetch<Stats>("/api/v1/stats"),
    apiFetchList<JobSummary>(`/api/v1/jobs?${jobQuery.toString()}`),
    apiFetchList<Worker>("/api/v1/workers"),
    apiFetchList<SourceSession>("/api/v1/sessions"),
  ]);

  return (
    <div className="space-y-10">
      <section className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        <div className="rounded border border-slate-200 bg-white p-3">
          <div className="text-xs text-slate-500">Anúncios</div>
          <div className="text-xl font-semibold">{stats.total_listings}</div>
        </div>
        <div className="rounded border border-slate-200 bg-white p-3">
          <div className="text-xs text-slate-500">Buscas ativas</div>
          <div className="text-xl font-semibold">{stats.enabled_searches}</div>
        </div>
        {Object.entries(stats.jobs_last_24h).map(([status, count]) => (
          <div key={status} className="rounded border border-slate-200 bg-white p-3">
            <div className="text-xs text-slate-500">Jobs ({status}, 24h)</div>
            <div className="text-xl font-semibold">{count}</div>
          </div>
        ))}
      </section>

      <section>
        <h2 className="mb-3 text-lg font-medium">Sessões</h2>
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-slate-200 text-left text-slate-500">
              <th className="py-2">Fonte</th>
              <th>Status</th>
              <th>Nota</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {sessions.map((s) => (
              <tr key={s.source} className="border-b border-slate-100">
                <td className="py-2">{s.source}</td>
                <td>{s.status}</td>
                <td>{s.note ?? "—"}</td>
                <td className="text-right">
                  {s.status !== "ok" && (
                    <form action={resumeSessionAction.bind(null, s.source)}>
                      <button type="submit" className="rounded bg-slate-900 px-2 py-1 text-xs font-medium text-white">
                        retomar
                      </button>
                    </form>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section>
        <h2 className="mb-3 text-lg font-medium">Workers</h2>
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-slate-200 text-left text-slate-500">
              <th className="py-2">ID</th>
              <th>Host</th>
              <th>Status</th>
              <th>Versão</th>
              <th>OK / Falhas</th>
              <th>Último heartbeat</th>
            </tr>
          </thead>
          <tbody>
            {workers.map((w) => (
              <tr key={w.id} className="border-b border-slate-100">
                <td className="py-2">{w.id}</td>
                <td>{w.hostname}</td>
                <td>{w.status}</td>
                <td>{w.crawler_version}</td>
                <td>
                  {w.jobs_ok} / {w.jobs_failed}
                </td>
                <td>{new Date(w.last_heartbeat_at).toLocaleString("pt-BR")}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {workers.length === 0 && <p className="mt-2 text-slate-500">Nenhum worker registrado ainda.</p>}
      </section>

      <section>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-lg font-medium">Jobs</h2>
          <form className="flex gap-2 text-sm">
            <select name="status" defaultValue={params.status ?? ""} className="rounded border border-slate-300 px-2 py-1">
              <option value="">Todos os status</option>
              <option value="queued">queued</option>
              <option value="running">running</option>
              <option value="succeeded">succeeded</option>
              <option value="dead">dead</option>
            </select>
            <select
              name="error_kind"
              defaultValue={params.error_kind ?? ""}
              className="rounded border border-slate-300 px-2 py-1"
            >
              <option value="">Qualquer erro</option>
              <option value="network">network</option>
              <option value="timeout">timeout</option>
              <option value="rate_limited">rate_limited</option>
              <option value="checkpoint">checkpoint</option>
              <option value="login_required">login_required</option>
              <option value="layout_changed">layout_changed</option>
            </select>
            <button type="submit" className="rounded bg-slate-900 px-3 py-1 font-medium text-white">
              Filtrar
            </button>
          </form>
        </div>
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-slate-200 text-left text-slate-500">
              <th className="py-2">Fonte</th>
              <th>Status</th>
              <th>Tentativas</th>
              <th>Erro</th>
              <th>Criado em</th>
            </tr>
          </thead>
          <tbody>
            {jobs.map((j) => (
              <tr key={j.id} className="border-b border-slate-100">
                <td className="py-2">{j.source}</td>
                <td>{j.status}</td>
                <td>{j.attempts}</td>
                <td>{j.error_kind ?? "—"}</td>
                <td>{new Date(j.created_at).toLocaleString("pt-BR")}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {jobs.length === 0 && <p className="mt-2 text-slate-500">Nenhum job encontrado.</p>}
      </section>
    </div>
  );
}
```

- [ ] **Step 3: Verificar**

```bash
cd frontend && npm run build
```
(or `docker compose build dashboard`)

```bash
docker compose up -d --build dashboard
curl -s http://localhost:3000/health | grep -o "Workers"
curl -s "http://localhost:3000/health?status=dead" | grep -o "dead"
```
Expected: both succeed; the page shows real data from the running stack (jobs/workers/sessions seeded by earlier plans' testing).

If any session shows a status other than `ok`, manually click "retomar" in a browser and confirm the row flips to `ok` after the page reloads (Server Action + `revalidatePath` round-trip).

- [ ] **Step 4: Commit** — same rule as Task 1 Step 12.

```bash
git add frontend/app/health/
git commit -m "feat(dashboard): add health page with stats, sessions, workers and jobs"
```

---

### Task 5: End-to-end verification against the real running stack

No new code — this closes out the plan by confirming the whole dashboard works together against the actual data already in the database (the seeded test search, and whatever real searches/regions exist from earlier manual testing).

- [ ] **Step 1**: `docker compose up -d --build dashboard` (picks up everything from Tasks 1-4).
- [ ] **Step 2**: Open `http://localhost:3000/` in a browser. Confirm: the top nav shows 3 tabs (Anúncios/Buscas/Saúde) with the active one underlined; the Anúncios page shows real listings as cards with photos (or the 🚗 placeholder for ones without `image_url`); the filter form works (try filtering by state and confirm the result set narrows).
- [ ] **Step 3**: Click a listing card, confirm the detail page shows the price history section and a working "Ver no Facebook Marketplace" link.
- [ ] **Step 4**: Go to `/searches`, confirm the real search(es) created in earlier manual testing show up with their correct keyword/region names. Create a new search end-to-end through the UI, confirm it appears, edit it, confirm the change sticks, delete it, confirm it's gone.
- [ ] **Step 5**: Go to `/health`, confirm the stats strip, sessions table, workers table and jobs table all show real data (not empty/error states) matching what `psql`/earlier `curl` checks showed.
- [ ] **Step 6**: Check `docker compose logs dashboard --tail 30` — confirm no unhandled exceptions or stack traces, only normal Next.js request logs.
- [ ] **Step 7**: Report back to the user: does every page load without an error boundary, and does every action (create/edit/delete search, create/edit region, resume session) work as expected against the real backend?
