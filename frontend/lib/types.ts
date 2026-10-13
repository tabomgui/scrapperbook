export interface Region {
  id: string;
  name: string;
  kind: string;
  city?: string;
  state?: string;
  lat?: number;
  lng?: number;
  radius_km?: number;
  fb_location?: string;
  enabled: boolean;
}

export interface Search {
  id: string;
  name: string;
  keyword: string;
  enabled: boolean;
  priority: number;
  frequency_seconds: number;
  filters: unknown;
  alert: unknown;
  region_ids: string[];
}

export interface Listing {
  id: number;
  source: string;
  external_id: string;
  url: string;
  title: string;
  description?: string;
  price_cents?: number;
  currency?: string;
  city?: string;
  state?: string;
  image_url?: string;
  seller_name?: string;
  status: string;
  first_seen_at: string;
  last_seen_at: string;
}

export interface Snapshot {
  price_cents?: number;
  title: string;
  status: string;
  captured_at: string;
}

export interface JobSummary {
  id: string;
  type: string;
  source: string;
  search_id?: string;
  region_id?: string;
  status: string;
  attempts: number;
  error_kind?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
}

export interface Worker {
  id: string;
  hostname: string;
  status: string;
  crawler_version: string;
  sources: string[] | null;
  current_job_id?: string;
  jobs_ok: number;
  jobs_failed: number;
  cooldown_until?: string;
  registered_at: string;
  last_heartbeat_at: string;
}

export interface SourceSession {
  source: string;
  status: string;
  last_checkpoint_at?: string;
  cooldown_until?: string;
  note?: string;
}

export interface Stats {
  total_listings: number;
  listings_by_state: Record<string, number>;
  enabled_searches: number;
  jobs_last_24h: Record<string, number>;
  workers_by_status: Record<string, number>;
}
