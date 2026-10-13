"use client";

import { useActionState } from "react";
import type { Region } from "@/lib/types";
import type { ActionResult } from "@/app/searches/actions";

type Props = {
  action: (formData: FormData) => Promise<ActionResult>;
  initial?: Region;
};

const initialState: ActionResult = { ok: true };

export function RegionForm({ action, initial }: Props) {
  const [state, formAction, isPending] = useActionState(
    async (_prev: ActionResult, formData: FormData) => action(formData),
    initialState,
  );

  return (
    <form action={formAction} className="max-w-md space-y-3">
      {!initial && (
        <div>
          <label className="block text-sm font-medium">ID (identificador único, ex: curitiba-pr)</label>
          <input name="id" required className="mt-1 w-full rounded border border-slate-300 px-2 py-1" />
        </div>
      )}
      <div>
        <label className="block text-sm font-medium">Nome</label>
        <input
          name="name"
          defaultValue={initial?.name}
          required
          className="mt-1 w-full rounded border border-slate-300 px-2 py-1"
        />
      </div>
      <div>
        <label className="block text-sm font-medium">Tipo</label>
        <select name="kind" defaultValue={initial?.kind ?? "city"} className="mt-1 w-full rounded border border-slate-300 px-2 py-1">
          <option value="city">Cidade</option>
          <option value="point">Ponto</option>
          <option value="state">Estado</option>
        </select>
      </div>
      <div>
        <label className="block text-sm font-medium">Cidade</label>
        <input name="city" defaultValue={initial?.city} className="mt-1 w-full rounded border border-slate-300 px-2 py-1" />
      </div>
      <div>
        <label className="block text-sm font-medium">Estado (UF)</label>
        <input name="state" defaultValue={initial?.state} className="mt-1 w-full rounded border border-slate-300 px-2 py-1" />
      </div>
      <div>
        <label className="block text-sm font-medium">fb_location (slug do Facebook)</label>
        <input
          name="fb_location"
          defaultValue={initial?.fb_location}
          className="mt-1 w-full rounded border border-slate-300 px-2 py-1"
        />
      </div>
      <div className="flex items-center gap-2">
        <input type="checkbox" name="enabled" id="enabled" defaultChecked={initial?.enabled ?? true} />
        <label htmlFor="enabled" className="text-sm">
          Ativa
        </label>
      </div>
      {!state.ok && <p className="text-sm text-red-600">{state.error}</p>}
      <button
        type="submit"
        disabled={isPending}
        className="rounded bg-slate-900 px-3 py-1 text-sm font-medium text-white disabled:opacity-50"
      >
        {isPending ? "Salvando..." : "Salvar"}
      </button>
    </form>
  );
}
