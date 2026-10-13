-- +goose Up
CREATE TABLE search_regions (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('city', 'point', 'state')),
    city        text,
    state       text,
    lat         double precision,
    lng         double precision,
    radius_km   integer,
    fb_location text,
    enabled     boolean NOT NULL DEFAULT true
);

CREATE TABLE search_definitions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    keyword     text NOT NULL,
    enabled     boolean NOT NULL DEFAULT true,
    priority    smallint NOT NULL DEFAULT 0,
    frequency   interval NOT NULL DEFAULT '1 hour',
    next_run_at timestamptz NOT NULL DEFAULT now(),
    filters     jsonb NOT NULL DEFAULT '{}',
    alert       jsonb NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX search_definitions_due ON search_definitions (next_run_at) WHERE enabled;

CREATE TABLE search_definition_regions (
    search_id       uuid NOT NULL REFERENCES search_definitions (id) ON DELETE CASCADE,
    region_id       text NOT NULL REFERENCES search_regions (id),
    baseline_done   boolean NOT NULL DEFAULT false,
    last_success_at timestamptz,
    PRIMARY KEY (search_id, region_id)
);

CREATE TABLE jobs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    type          text NOT NULL CHECK (type IN ('search', 'enrich')),
    source        text NOT NULL,
    search_id     uuid REFERENCES search_definitions (id) ON DELETE CASCADE,
    region_id     text REFERENCES search_regions (id),
    payload       jsonb NOT NULL,
    status        text NOT NULL DEFAULT 'queued'
                  CHECK (status IN ('queued', 'running', 'succeeded', 'dead')),
    priority      smallint NOT NULL DEFAULT 0,
    attempts      integer NOT NULL DEFAULT 0,
    max_attempts  integer NOT NULL DEFAULT 4,
    run_after     timestamptz NOT NULL,
    lease_until   timestamptz,
    worker_id     text,
    error_kind    text,
    error_message text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    started_at    timestamptz,
    finished_at   timestamptz
);
-- Postgres unique indexes treat NULL as distinct from every other NULL, so this only
-- dedupes jobs that have both search_id and region_id set (i.e. type = 'search').
-- A future 'enrich' job type (which has neither) will need its own dedupe key.
CREATE UNIQUE INDEX jobs_one_in_flight ON jobs (source, search_id, region_id)
    WHERE status IN ('queued', 'running');
CREATE INDEX jobs_claim ON jobs (status, source, priority DESC, run_after);
CREATE INDEX jobs_started ON jobs (source, started_at);

CREATE TABLE raw_captures (
    id              bigserial PRIMARY KEY,
    job_id          uuid NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    source          text NOT NULL,
    external_id     text NOT NULL,
    crawler_version text NOT NULL,
    parser_version  text NOT NULL,
    payload         jsonb NOT NULL,
    captured_at     timestamptz NOT NULL
);
CREATE INDEX raw_captures_captured_at ON raw_captures (captured_at);

CREATE TABLE listings (
    id                 bigserial PRIMARY KEY,
    source             text NOT NULL,
    external_id        text NOT NULL,
    url                text NOT NULL,
    title              text NOT NULL,
    description        text,
    price_cents        bigint,
    currency           text,
    location_text      text,
    city               text,
    state              text,
    lat                double precision,
    lng                double precision,
    image_url          text,
    seller_external_id text,
    seller_name        text,
    status             text NOT NULL DEFAULT 'active'
                       CHECK (status IN ('active', 'pending', 'sold', 'unseen')),
    fingerprint        text NOT NULL,
    content_hash       text NOT NULL,
    first_seen_at      timestamptz NOT NULL,
    last_seen_at       timestamptz NOT NULL,
    last_changed_at    timestamptz NOT NULL,
    raw_data           jsonb NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, external_id)
);
CREATE INDEX listings_first_seen ON listings (first_seen_at);
CREATE INDEX listings_last_seen ON listings (last_seen_at);
CREATE INDEX listings_price ON listings (price_cents);
CREATE INDEX listings_location ON listings (state, city);
CREATE INDEX listings_seller ON listings (seller_external_id);
CREATE INDEX listings_fingerprint ON listings (fingerprint);

CREATE TABLE listing_snapshots (
    id          bigserial PRIMARY KEY,
    listing_id  bigint NOT NULL REFERENCES listings (id) ON DELETE CASCADE,
    price_cents bigint,
    title       text NOT NULL,
    description text,
    status      text NOT NULL,
    job_id      uuid REFERENCES jobs (id) ON DELETE SET NULL,
    captured_at timestamptz NOT NULL,
    raw_data    jsonb NOT NULL
);
CREATE INDEX listing_snapshots_listing ON listing_snapshots (listing_id, captured_at);

CREATE TABLE search_listings (
    search_id     uuid NOT NULL REFERENCES search_definitions (id) ON DELETE CASCADE,
    listing_id    bigint NOT NULL REFERENCES listings (id) ON DELETE CASCADE,
    first_seen_at timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL,
    PRIMARY KEY (search_id, listing_id)
);

CREATE TABLE notifications (
    id              bigserial PRIMARY KEY,
    dedupe_key      text NOT NULL UNIQUE,
    kind            text NOT NULL CHECK (kind IN ('new', 'price_drop', 'system')),
    listing_id      bigint REFERENCES listings (id) ON DELETE CASCADE,
    search_id       uuid REFERENCES search_definitions (id) ON DELETE CASCADE,
    price_cents     bigint,
    old_price_cents bigint,
    message         text,
    channel         text NOT NULL DEFAULT 'discord',
    attempts        integer NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    sent_at         timestamptz
);
CREATE INDEX notifications_pending ON notifications (created_at) WHERE sent_at IS NULL;

CREATE TABLE workers (
    id                text PRIMARY KEY,
    hostname          text NOT NULL,
    status            text NOT NULL CHECK (status IN
                      ('starting', 'healthy', 'busy', 'idle', 'error', 'cooldown', 'stopped')),
    crawler_version   text NOT NULL,
    sources           text[] NOT NULL DEFAULT '{}',
    current_job_id    uuid REFERENCES jobs (id) ON DELETE SET NULL,
    jobs_ok           integer NOT NULL DEFAULT 0,
    jobs_failed       integer NOT NULL DEFAULT 0,
    cooldown_until    timestamptz,
    registered_at     timestamptz NOT NULL,
    last_heartbeat_at timestamptz NOT NULL
);

CREATE TABLE source_sessions (
    source             text PRIMARY KEY,
    status             text NOT NULL DEFAULT 'ok' CHECK (status IN ('ok', 'needs_attention')),
    last_checkpoint_at timestamptz,
    cooldown_until     timestamptz,
    note               text,
    updated_at         timestamptz NOT NULL DEFAULT now()
);
INSERT INTO source_sessions (source) VALUES ('facebook_marketplace');

-- +goose Down
DROP TABLE source_sessions;
DROP TABLE workers;
DROP TABLE notifications;
DROP TABLE search_listings;
DROP TABLE listing_snapshots;
DROP TABLE listings;
DROP TABLE raw_captures;
DROP TABLE jobs;
DROP TABLE search_definition_regions;
DROP TABLE search_definitions;
DROP TABLE search_regions;
