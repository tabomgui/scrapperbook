"use server";

import { revalidatePath } from "next/cache";
import { redirect } from "next/navigation";
import { apiFetch, ApiError } from "@/lib/api";

export type ActionResult = { ok: true } | { ok: false; error: string };

function parseFrequencyMinutes(raw: FormDataEntryValue | null): number {
  return Math.round(Number(raw) * 60);
}

// Unchecked checkboxes are omitted from FormData entirely (not sent as
// "false"), so `formData.get("enabled") === "on"` is what correctly
// distinguishes checked from unchecked — don't "simplify" this check.
export async function createSearchAction(formData: FormData): Promise<ActionResult> {
  const body = {
    name: String(formData.get("name") ?? ""),
    keyword: String(formData.get("keyword") ?? ""),
    enabled: formData.get("enabled") === "on",
    priority: Number(formData.get("priority") ?? 0),
    frequency_seconds: parseFrequencyMinutes(formData.get("frequency_minutes")),
    region_ids: formData.getAll("region_ids").map(String),
  };
  try {
    await apiFetch("/api/v1/searches", { method: "POST", body: JSON.stringify(body) });
  } catch (err) {
    if (err instanceof ApiError) {
      return { ok: false, error: err.message };
    }
    console.error("createSearchAction failed unexpectedly:", err);
    return { ok: false, error: "erro inesperado" };
  }
  revalidatePath("/searches");
  redirect("/searches");
}

export async function updateSearchAction(id: string, formData: FormData): Promise<ActionResult> {
  const body = {
    name: String(formData.get("name") ?? ""),
    keyword: String(formData.get("keyword") ?? ""),
    enabled: formData.get("enabled") === "on",
    priority: Number(formData.get("priority") ?? 0),
    frequency_seconds: parseFrequencyMinutes(formData.get("frequency_minutes")),
    region_ids: formData.getAll("region_ids").map(String),
  };
  try {
    await apiFetch(`/api/v1/searches/${id}`, { method: "PATCH", body: JSON.stringify(body) });
  } catch (err) {
    if (err instanceof ApiError) {
      return { ok: false, error: err.message };
    }
    console.error("updateSearchAction failed unexpectedly:", err);
    return { ok: false, error: "erro inesperado" };
  }
  revalidatePath("/searches");
  redirect("/searches");
}

export async function deleteSearchAction(id: string): Promise<void> {
  await apiFetch(`/api/v1/searches/${id}`, { method: "DELETE" });
  revalidatePath("/searches");
}

export async function runSearchAction(id: string): Promise<void> {
  await apiFetch(`/api/v1/searches/${id}/run`, { method: "POST" });
  revalidatePath("/searches");
}

export async function createRegionAction(formData: FormData): Promise<ActionResult> {
  const body = {
    id: String(formData.get("id") ?? ""),
    name: String(formData.get("name") ?? ""),
    kind: String(formData.get("kind") ?? "city"),
    city: String(formData.get("city") ?? "") || undefined,
    state: String(formData.get("state") ?? "") || undefined,
    fb_location: String(formData.get("fb_location") ?? "") || undefined,
    enabled: formData.get("enabled") === "on",
  };
  try {
    await apiFetch("/api/v1/regions", { method: "POST", body: JSON.stringify(body) });
  } catch (err) {
    if (err instanceof ApiError) {
      return { ok: false, error: err.message };
    }
    console.error("createRegionAction failed unexpectedly:", err);
    return { ok: false, error: "erro inesperado" };
  }
  revalidatePath("/searches");
  redirect("/searches");
}

export async function updateRegionAction(id: string, formData: FormData): Promise<ActionResult> {
  const body = {
    name: String(formData.get("name") ?? ""),
    city: String(formData.get("city") ?? "") || undefined,
    state: String(formData.get("state") ?? "") || undefined,
    fb_location: String(formData.get("fb_location") ?? "") || undefined,
    enabled: formData.get("enabled") === "on",
  };
  try {
    await apiFetch(`/api/v1/regions/${id}`, { method: "PATCH", body: JSON.stringify(body) });
  } catch (err) {
    if (err instanceof ApiError) {
      return { ok: false, error: err.message };
    }
    console.error("updateRegionAction failed unexpectedly:", err);
    return { ok: false, error: "erro inesperado" };
  }
  revalidatePath("/searches");
  redirect("/searches");
}
