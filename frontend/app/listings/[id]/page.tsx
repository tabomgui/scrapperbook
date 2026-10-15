import { notFound } from "next/navigation";
import { apiFetch, ApiError } from "@/lib/api";
import { formatPrice } from "@/lib/format";
import type { Listing, Snapshot } from "@/lib/types";
import { hideListingAction, unhideListingAction } from "../actions";

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
      <div className="mb-6 flex gap-2">
        <a
          href={data.listing.url}
          target="_blank"
          rel="noreferrer"
          className="inline-block rounded bg-slate-900 px-3 py-1 text-sm font-medium text-white"
        >
          Ver no Facebook Marketplace
        </a>
        <form action={(data.listing.hidden ? unhideListingAction : hideListingAction).bind(null, data.listing.id)}>
          <button
            type="submit"
            className="rounded border border-slate-300 px-3 py-1 text-sm font-medium text-slate-700"
          >
            {data.listing.hidden ? "mostrar" : "ocultar"}
          </button>
        </form>
      </div>

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
