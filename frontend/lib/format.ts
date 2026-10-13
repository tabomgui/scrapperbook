export function formatPrice(cents?: number): string {
  if (cents == null) return "sem preço";
  return `R$ ${(cents / 100).toLocaleString("pt-BR", { minimumFractionDigits: 2 })}`;
}

export function timeAgo(iso: string): string {
  const diffMs = Date.now() - new Date(iso).getTime();
  const hours = Math.floor(diffMs / 3_600_000);
  if (hours < 1) return "visto agora";
  if (hours < 24) return `visto há ${hours}h`;
  return `visto há ${Math.floor(hours / 24)}d`;
}
