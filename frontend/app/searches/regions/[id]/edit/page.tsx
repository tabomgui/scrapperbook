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
