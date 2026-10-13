const API_BASE_URL = process.env.API_BASE_URL ?? "http://localhost:8080";

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE_URL}${path}`, {
    ...init,
    cache: "no-store",
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  if (!res.ok) {
    let message = `request failed with status ${res.status}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body?.error) message = body.error;
    } catch {
      // response had no JSON body; keep the generic message
    }
    throw new ApiError(res.status, message);
  }
  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}

export function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  return request<T>(path, init);
}

export async function apiFetchList<T>(path: string, init?: RequestInit): Promise<T[]> {
  const result = await request<T[] | null>(path, init);
  return result ?? [];
}
