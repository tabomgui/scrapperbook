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
