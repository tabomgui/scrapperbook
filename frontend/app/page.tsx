import Link from "next/link";
import { apiFetchList } from "@/lib/api";
import { formatPrice, timeAgo } from "@/lib/format";
import type { Listing, Search } from "@/lib/types";
import { hideListingAction, unhideListingAction } from "./listings/actions";

type SearchParams = {
  search_id?: string;
  state?: string;
  city?: string;
  min_price_reais?: string;
  max_price_reais?: string;
  status?: string;
  hidden?: string;
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

  const [listings, searches] = await Promise.all([
    apiFetchList<Listing>(`/api/v1/listings?${query.toString()}`),
    apiFetchList<Search>("/api/v1/searches"),
  ]);

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
        <select name="status" defaultValue={params.status ?? ""} className="rounded border border-slate-300 px-2 py-1">
          <option value="">Todos os status</option>
          <option value="active">Ativo</option>
          <option value="unseen">Sumiu</option>
        </select>
        <label className="flex items-center gap-1">
          <input type="checkbox" name="hidden" value="all" defaultChecked={params.hidden === "all"} />
          mostrar ocultos
        </label>
        <button type="submit" className="rounded bg-slate-900 px-3 py-1 font-medium text-white">
          Filtrar
        </button>
      </form>

      {listings.length === 0 && <p className="text-slate-500">Nenhum anúncio encontrado com esses filtros.</p>}

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
    </div>
  );
}
