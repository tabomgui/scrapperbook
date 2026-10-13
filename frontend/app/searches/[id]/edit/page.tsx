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
