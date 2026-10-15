import Link from "next/link";
import { apiFetch } from "@/lib/api";
import { formatPrice, timeAgo } from "@/lib/format";
import type { Listing, Search } from "@/lib/types";
import { hideListingAction, unhideListingAction } from "./listings/actions";

const PAGE_SIZE = 24;

type SearchParams = {
  search_id?: string;
  state?: string;
  city?: string;
  min_price_reais?: string;
  max_price_reais?: string;
  status?: string;
  hidden?: string;
  first_seen_after?: string;
  first_seen_before?: string;
  sort?: string;
  page?: string;
  view?: string;
};

const SORT_OPTIONS: Array<{ value: string; label: string }> = [
  { value: "-first_seen_at", label: "Mais recente" },
  { value: "first_seen_at", label: "Mais antigo" },
  { value: "-price_cents", label: "Maior preço" },
  { value: "price_cents", label: "Menor preço" },
  { value: "-last_seen_at", label: "Visto por último" },
  { value: "last_seen_at", label: "Visto há mais tempo" },
];

function withParams(params: SearchParams, overrides: Record<string, string | undefined>): string {
  const merged: Record<string, string | undefined> = { ...params, ...overrides };
  const sp = new URLSearchParams();
  for (const [key, value] of Object.entries(merged)) {
    if (value) sp.set(key, value);
  }
  const qs = sp.toString();
  return qs ? `?${qs}` : "";
}

function pageNumbers(totalPages: number, current: number): Array<number | "…"> {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, i) => i + 1);
  }
  const keep = new Set<number>([1, totalPages, current - 2, current - 1, current, current + 1, current + 2]);
  const sorted = [...keep].filter((p) => p >= 1 && p <= totalPages).sort((a, b) => a - b);
  const out: Array<number | "…"> = [];
  let prev = 0;
  for (const p of sorted) {
    if (prev && p - prev > 1) out.push("…");
    out.push(p);
    prev = p;
  }
  return out;
}

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
    const reais = Number(params.min_price_reais);
    if (Number.isFinite(reais)) {
      query.set("min_price", String(Math.round(reais * 100)));
    }
  }
  if (params.max_price_reais) {
    const reais = Number(params.max_price_reais);
    if (Number.isFinite(reais)) {
      query.set("max_price", String(Math.round(reais * 100)));
    }
  }
  if (params.status) query.set("status", params.status);
  if (params.hidden) query.set("hidden", params.hidden);
  if (params.first_seen_after) query.set("first_seen_after", params.first_seen_after);
  if (params.first_seen_before) query.set("first_seen_before", params.first_seen_before);
  if (params.sort) query.set("sort", params.sort);

  const pageNum = Number(params.page);
  const page = Number.isFinite(pageNum) && pageNum >= 1 ? Math.floor(pageNum) : 1;
  query.set("limit", String(PAGE_SIZE));
  query.set("offset", String((page - 1) * PAGE_SIZE));

  const view = params.view === "list" ? "list" : "grid";

  const [{ listings, total }, searches] = await Promise.all([
    apiFetch<{ listings: Listing[]; total: number }>(`/api/v1/listings?${query.toString()}`),
    apiFetch<Search[]>("/api/v1/searches"),
  ]);
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <div>
      <h1 className="mb-4 text-xl font-semibold">Anúncios</h1>
      <form className="mb-6 flex flex-wrap items-center gap-3 rounded border border-slate-200 bg-white p-4 text-sm">
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
        <label className="flex items-center gap-1 text-slate-500">
          de
          <input
            name="first_seen_after"
            type="date"
            defaultValue={params.first_seen_after ?? ""}
            className="rounded border border-slate-300 px-2 py-1"
          />
        </label>
        <label className="flex items-center gap-1 text-slate-500">
          até
          <input
            name="first_seen_before"
            type="date"
            defaultValue={params.first_seen_before ?? ""}
            className="rounded border border-slate-300 px-2 py-1"
          />
        </label>
        <select name="status" defaultValue={params.status ?? ""} className="rounded border border-slate-300 px-2 py-1">
          <option value="">Todos os status</option>
          <option value="active">Ativo</option>
          <option value="unseen">Sumiu</option>
        </select>
        <select name="sort" defaultValue={params.sort ?? SORT_OPTIONS[0].value} className="rounded border border-slate-300 px-2 py-1">
          {SORT_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
        <label className="flex items-center gap-1">
          <input type="checkbox" name="hidden" value="all" defaultChecked={params.hidden === "all"} />
          mostrar ocultos
        </label>
        <button type="submit" className="rounded bg-slate-900 px-3 py-1 font-medium text-white">
          Filtrar
        </button>
      </form>

      <div className="mb-4 flex items-center justify-between text-sm">
        <span className="text-slate-500">{total} anúncio{total === 1 ? "" : "s"}</span>
        <div className="flex gap-1">
          <Link
            href={withParams(params, { view: "grid" })}
            className={`rounded border px-2 py-1 ${view === "grid" ? "border-slate-900 bg-slate-900 text-white" : "border-slate-300 text-slate-600"}`}
          >
            grade
          </Link>
          <Link
            href={withParams(params, { view: "list" })}
            className={`rounded border px-2 py-1 ${view === "list" ? "border-slate-900 bg-slate-900 text-white" : "border-slate-300 text-slate-600"}`}
          >
            lista
          </Link>
        </div>
      </div>

      {listings.length === 0 && <p className="text-slate-500">Nenhum anúncio encontrado com esses filtros.</p>}

      {view === "grid" ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {listings.map((l) => (
            <div key={l.id} className="rounded border border-slate-200 bg-white p-3 hover:border-slate-400">
              <Link href={`/listings/${l.id}`}>
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
              <form action={(l.hidden ? unhideListingAction : hideListingAction).bind(null, l.id)} className="mt-2">
                <button type="submit" className="text-xs text-slate-500 hover:text-slate-900">
                  {l.hidden ? "mostrar" : "ocultar"}
                </button>
              </form>
            </div>
          ))}
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          {listings.map((l) => (
            <div
              key={l.id}
              className="flex items-center gap-3 rounded border border-slate-200 bg-white p-2 hover:border-slate-400"
            >
              <Link href={`/listings/${l.id}`} className="flex flex-1 items-center gap-3 overflow-hidden">
                <div className="flex h-12 w-12 shrink-0 items-center justify-center overflow-hidden rounded bg-slate-100">
                  {l.image_url ? (
                    // eslint-disable-next-line @next/next/no-img-element -- arbitrary external CDN hosts
                    <img src={l.image_url} alt={l.title} className="h-full w-full object-cover" />
                  ) : (
                    <span className="text-xl">🚗</span>
                  )}
                </div>
                <div className="flex flex-1 items-center gap-3 overflow-hidden text-sm">
                  <span className="min-w-0 flex-1 truncate font-medium">{l.title}</span>
                  <span className="shrink-0 text-slate-500">{formatPrice(l.price_cents)}</span>
                  <span className="shrink-0 text-slate-500">
                    {l.city ?? "?"}/{l.state ?? "?"}
                  </span>
                  <span className="shrink-0 text-xs text-slate-400">{timeAgo(l.last_seen_at)}</span>
                </div>
              </Link>
              <form action={(l.hidden ? unhideListingAction : hideListingAction).bind(null, l.id)}>
                <button type="submit" className="shrink-0 text-xs text-slate-500 hover:text-slate-900">
                  {l.hidden ? "mostrar" : "ocultar"}
                </button>
              </form>
            </div>
          ))}
        </div>
      )}

      {totalPages > 1 && (
        <nav className="mt-6 flex gap-1 text-sm">
          {pageNumbers(totalPages, page).map((p, i) =>
            p === "…" ? (
              <span key={`ellipsis-${i}`} className="px-2 py-1 text-slate-400">
                …
              </span>
            ) : (
              <Link
                key={p}
                href={withParams(params, { page: String(p) })}
                className={`rounded border px-3 py-1 ${p === page ? "border-slate-900 bg-slate-900 text-white" : "border-slate-300 text-slate-600"}`}
              >
                {p}
              </Link>
            ),
          )}
        </nav>
      )}
    </div>
  );
}
