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
