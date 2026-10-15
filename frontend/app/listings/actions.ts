"use server";

import { revalidatePath } from "next/cache";
import { apiFetch } from "@/lib/api";

export async function hideListingAction(id: number): Promise<void> {
  await apiFetch(`/api/v1/listings/${id}/hide`, { method: "POST" });
  revalidatePath("/");
  revalidatePath(`/listings/${id}`);
}

export async function unhideListingAction(id: number): Promise<void> {
  await apiFetch(`/api/v1/listings/${id}/unhide`, { method: "POST" });
  revalidatePath("/");
  revalidatePath(`/listings/${id}`);
}
