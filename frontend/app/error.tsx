"use client";

export default function Error({ error, reset }: { error: Error; reset: () => void }) {
  return (
    <div className="rounded border border-red-200 bg-red-50 p-4 text-red-800">
      <p className="font-medium">Algo deu errado.</p>
      <p className="text-sm">{error.message}</p>
      <button onClick={reset} className="mt-2 rounded bg-red-800 px-3 py-1 text-sm text-white">
        Tentar de novo
      </button>
    </div>
  );
}
