export default function NotFound() {
  return (
    <div className="rounded border border-slate-200 bg-white p-4">
      <p className="font-medium">Não encontrado.</p>
      <a href="/" className="text-sm text-slate-500 hover:text-slate-900">
        ← voltar
      </a>
    </div>
  );
}
