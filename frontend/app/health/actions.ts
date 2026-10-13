"use server";

import { revalidatePath } from "next/cache";
import { apiFetch } from "@/lib/api";

export async function resumeSessionAction(source: string): Promise<void> {
  await apiFetch(`/api/v1/sessions/${source}/resume`, { method: "POST" });
  revalidatePath("/health");
}
