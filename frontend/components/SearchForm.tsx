"use client";

import { useActionState } from "react";
import type { Region, Search } from "@/lib/types";
import type { ActionResult } from "@/app/searches/actions";

type Props = {
  action: (formData: FormData) => Promise<ActionResult>;
  regions: Region[];
  initial?: Search;
};

const initialState: ActionResult = { ok: true };

export function SearchForm({ action, regions, initial }: Props) {
  const [state, formAction, isPending] = useActionState(
    async (_prev: ActionResult, formData: FormData) => action(formData),
    initialState,
  );

  return (
    <form action={formAction} className="max-w-md space-y-3">
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
        <label className="block text-sm font-medium">Palavra-chave</label>
        <input
          name="keyword"
          defaultValue={initial?.keyword}
          required
          className="mt-1 w-full rounded border border-slate-300 px-2 py-1"
        />
      </div>
      <div>
        <label className="block text-sm font-medium">Frequência (minutos)</label>
        <input
          name="frequency_minutes"
          type="number"
          min={1}
          defaultValue={initial ? Math.round(initial.frequency_seconds / 60) : 30}
          required
          className="mt-1 w-full rounded border border-slate-300 px-2 py-1"
        />
      </div>
      <div>
        <label className="block text-sm font-medium">Prioridade</label>
        <input
          name="priority"
          type="number"
          defaultValue={initial?.priority ?? 0}
          className="mt-1 w-full rounded border border-slate-300 px-2 py-1"
        />
      </div>
      <div>
        <label className="block text-sm font-medium">Regiões</label>
        <select
          name="region_ids"
          multiple
          defaultValue={initial?.region_ids}
          className="mt-1 w-full rounded border border-slate-300 px-2 py-1"
        >
          {regions.map((r) => (
            <option key={r.id} value={r.id}>
              {r.name}
            </option>
          ))}
        </select>
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
