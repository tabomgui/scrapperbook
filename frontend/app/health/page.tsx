import { apiFetch, apiFetchList } from "@/lib/api";
import type { JobSummary, SourceSession, Stats, Worker } from "@/lib/types";
import { resumeSessionAction } from "./actions";

export default async function HealthPage({
  searchParams,
}: {
  searchParams: Promise<{ status?: string; error_kind?: string }>;
}) {
  const params = await searchParams;
  const jobQuery = new URLSearchParams();
  if (params.status) jobQuery.set("status", params.status);
  if (params.error_kind) jobQuery.set("error_kind", params.error_kind);

  const [stats, jobs, workers, sessions] = await Promise.all([
    apiFetch<Stats>("/api/v1/stats"),
    apiFetchList<JobSummary>(`/api/v1/jobs?${jobQuery.toString()}`),
    apiFetchList<Worker>("/api/v1/workers"),
    apiFetchList<SourceSession>("/api/v1/sessions"),
  ]);

  return (
    <div className="space-y-10">
      <section className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        <div className="rounded border border-slate-200 bg-white p-3">
          <div className="text-xs text-slate-500">Anúncios</div>
          <div className="text-xl font-semibold">{stats.total_listings}</div>
        </div>
        <div className="rounded border border-slate-200 bg-white p-3">
          <div className="text-xs text-slate-500">Buscas ativas</div>
          <div className="text-xl font-semibold">{stats.enabled_searches}</div>
        </div>
        {Object.entries(stats.jobs_last_24h).map(([status, count]) => (
          <div key={status} className="rounded border border-slate-200 bg-white p-3">
            <div className="text-xs text-slate-500">Jobs ({status}, 24h)</div>
            <div className="text-xl font-semibold">{count}</div>
          </div>
        ))}
      </section>

      <section>
        <h2 className="mb-3 text-lg font-medium">Sessões</h2>
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-slate-200 text-left text-slate-500">
              <th className="py-2">Fonte</th>
              <th>Status</th>
              <th>Nota</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {sessions.map((s) => (
              <tr key={s.source} className="border-b border-slate-100">
                <td className="py-2">{s.source}</td>
                <td>{s.status}</td>
                <td>{s.note ?? "—"}</td>
                <td className="text-right">
                  {s.status !== "ok" && (
                    <form action={resumeSessionAction.bind(null, s.source)}>
                      <button type="submit" className="rounded bg-slate-900 px-2 py-1 text-xs font-medium text-white">
                        retomar
                      </button>
                    </form>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section>
        <h2 className="mb-3 text-lg font-medium">Workers</h2>
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-slate-200 text-left text-slate-500">
              <th className="py-2">ID</th>
              <th>Host</th>
              <th>Status</th>
              <th>Versão</th>
              <th>OK / Falhas</th>
              <th>Último heartbeat</th>
            </tr>
          </thead>
          <tbody>
            {workers.map((w) => (
              <tr key={w.id} className="border-b border-slate-100">
                <td className="py-2">{w.id}</td>
                <td>{w.hostname}</td>
                <td>{w.status}</td>
                <td>{w.crawler_version}</td>
                <td>
                  {w.jobs_ok} / {w.jobs_failed}
                </td>
                <td>{new Date(w.last_heartbeat_at).toLocaleString("pt-BR")}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {workers.length === 0 && <p className="mt-2 text-slate-500">Nenhum worker registrado ainda.</p>}
      </section>

      <section>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-lg font-medium">Jobs</h2>
          <form className="flex gap-2 text-sm">
            <select name="status" defaultValue={params.status ?? ""} className="rounded border border-slate-300 px-2 py-1">
              <option value="">Todos os status</option>
              <option value="queued">queued</option>
              <option value="running">running</option>
              <option value="succeeded">succeeded</option>
              <option value="dead">dead</option>
            </select>
            <select
              name="error_kind"
              defaultValue={params.error_kind ?? ""}
              className="rounded border border-slate-300 px-2 py-1"
            >
              <option value="">Qualquer erro</option>
              <option value="network">network</option>
              <option value="timeout">timeout</option>
              <option value="rate_limited">rate_limited</option>
              <option value="checkpoint">checkpoint</option>
              <option value="login_required">login_required</option>
              <option value="layout_changed">layout_changed</option>
            </select>
            <button type="submit" className="rounded bg-slate-900 px-3 py-1 font-medium text-white">
              Filtrar
            </button>
          </form>
        </div>
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-slate-200 text-left text-slate-500">
              <th className="py-2">Fonte</th>
              <th>Status</th>
              <th>Tentativas</th>
              <th>Erro</th>
              <th>Criado em</th>
            </tr>
          </thead>
          <tbody>
            {jobs.map((j) => (
              <tr key={j.id} className="border-b border-slate-100">
                <td className="py-2">{j.source}</td>
                <td>{j.status}</td>
                <td>{j.attempts}</td>
                <td>{j.error_kind ?? "—"}</td>
                <td>{new Date(j.created_at).toLocaleString("pt-BR")}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {jobs.length === 0 && <p className="mt-2 text-slate-500">Nenhum job encontrado.</p>}
      </section>
    </div>
  );
}
