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
