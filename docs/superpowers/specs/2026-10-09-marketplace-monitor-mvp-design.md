# Marketplace Monitor — Design do MVP

- **Data:** 2026-10-09
- **Status:** aprovado em brainstorming, aguardando revisão final
- **Origem:** SPEC "Plataforma Nacional de Monitoramento do Facebook Marketplace"

## 1. Contexto e decisões de produto

O SPEC original descreve uma plataforma nacional, distribuída e multi-source. Durante o brainstorming as seguintes decisões reduziram e direcionaram o escopo:

| Tema | Decisão |
|---|---|
| Uso | Pessoal/hobby. Single-user, sem autenticação na API, escala modesta. |
| Acesso ao Facebook | Sessão logada com a conta pessoal do usuário. |
| Consumo | Alertas push **e** dashboard enxuto, ambos no MVP. |
| Canal de alerta | Discord webhook, atrás de uma interface `Notifier`. |
| Categoria | Genérica (qualquer item). Atributos específicos ficam para enrichment/IA. |
| Onde roda | Máquina em casa, 24/7, IP residencial. |
| Arquitetura | Go core + worker Python + somente PostgreSQL (sem RabbitMQ/Redis no MVP). |
| Crawling | Playwright puro (async). Crawlee descartado. |

### 1.1 Riscos aceitos

- **Termos de uso da Meta:** automação com sessão logada viola os Automated Data Collection Terms. Risco aceito pelo usuário: bloqueio ou checkpoint da conta pessoal. Mitigações: volume baixo, intervalos com jitter, quiet hours, IP residencial, perfil persistente, parada imediata em checkpoint.
- **Limite estrutural de vazão:** uma conta equivale a uma sessão, com vazão baixa. O sistema **não** implementa pool de contas, contas falsas, rotação de identidade, resolução de CAPTCHA ou qualquer mecanismo de evasão. Escala vem de priorização e frequência adaptativa.
- **Fragilidade da fonte:** o Facebook muda frontend e `doc_id` de GraphQL com frequência. Mitigações: captura por formato da resposta, fallback para JSON embutido, raw armazenado para re-normalização, classificação de erro `layout_changed` com alerta.
- **LGPD:** uso doméstico pessoal está fora do escopo da lei (art. 4º, I). Os dados não são redistribuídos. Fixtures de teste commitadas devem ser sanitizadas (sem nomes reais de vendedores).

### 1.2 Pesquisa de projetos existentes

- **Marketscrape** (`Marketscrape/marketscrape-web`): abandonado desde 2024-10. Avalia uma URL isolada contra preços do eBay; não faz busca nem monitoramento. Nada reaproveitável além da ideia de ler `ld+json` em páginas de detalhe.
- **ai-marketplace-monitor** (`BoPeng/ai-marketplace-monitor`, AGPL-3.0, ativo): caso de uso mais próximo. Pontos fracos: seletores DOM posicionais, login a cada execução, sem histórico de preço, sem Discord nativo. Removeu o modo deslogado por instabilidade, o que confirma a decisão de usar sessão logada. Código não deve ser copiado (AGPL).
- **secondhand-mcp** e **facebook-marketplace-mcp**: melhores referências técnicas para a API GraphQL interna (`/api/graphql/`, `doc_id` rotativo, estrutura `marketplace_search.feed_units.edges[].node.listing`).
- **hyacinth**: bot Discord para Marketplace, parado desde 2024. Referência de design para ordenação por `creation_time_descend` com parada antecipada.

### 1.3 Por que não Crawlee

Crawlee para Python (1.x, estável) traz fila interna, session pool, rotação de sessão, autoscaling e storage local. Neste caso:

- a fila é externa (Go/Postgres), e encaixar o modelo "um job externo, uma execução, um resultado" no Crawlee exige contornos (`purge_request_queue`, `unique_key` por job, bridge de resultados);
- a rotação de sessão e de fingerprint conflita com o requisito de manter **uma** identidade logada consistente;
- a captura de GraphQL exige descer ao Playwright cru de qualquer forma.

Playwright puro com contexto persistente resolve o problema com bem menos código e menos superfície de erro.

### 1.4 Por que não RabbitMQ e Redis no MVP

Com uma sessão e poucas buscas por hora, uma tabela de jobs no Postgres com `SELECT … FOR UPDATE SKIP LOCKED` cobre fila, retry, backoff, lease e histórico. O rate limit é verificado no claim por contagem em SQL. O worker conversa apenas com a API HTTP do Go, então trocar a fila por RabbitMQ no futuro não altera o worker.

## 2. Arquitetura

```text
 Next.js (web) ──HTTP──► Go server ◄──HTTP── Python crawler (1 worker)
                          │   ▲               Playwright headed + Xvfb
                          │   │               perfil persistente (volume)
          scheduler ──────┤   │
          notifier ───────┤   └── register / heartbeat / claim / result / fail
          (Discord)       ▼
                      PostgreSQL (dados + fila de jobs)
```

### 2.1 Go: um binário no MVP

`cmd/server` executa API, scheduler e notifier como goroutines no mesmo processo. Os pacotes já são separados para permitir dividir em serviços depois. O Go é dono de todas as regras de negócio: agendamento, rate limit, retries, normalização, deduplicação, snapshots, alertas e persistência.

### 2.2 Python: um worker sem regras de negócio

O worker puxa um job, navega, captura dados brutos, classifica erros e devolve o resultado. Não decide deduplicação, não calcula alertas e não acessa o banco.

### 2.3 Sessão e login

- O Chromium roda **headed** dentro do container, sob Xvfb.
- `crawler login` sobe noVNC em `127.0.0.1:6080`. O usuário faz login manualmente uma vez, incluindo 2FA se houver.
- O perfil do Chromium fica em um volume Docker e é reutilizado com `launch_persistent_context`.
- Login e crawl acontecem no mesmo ambiente porque perfis do Chromium não são portáveis entre macOS e Linux (a criptografia de cookies difere).
- Nada de login automatizado. Nada de resolução automática de CAPTCHA ou 2FA.

### 2.4 Estrutura do monorepo

```text
scrapperbook/
├── compose.yaml
├── .env.example
├── backend/
│   ├── cmd/server/
│   ├── internal/
│   │   ├── search/           SearchDefinition, SearchRegion, expansão em jobs
│   │   ├── jobs/             fila Postgres (claim SKIP LOCKED, lease, retry, backoff)
│   │   ├── scheduler/        gocron v2: definições vencidas viram jobs
│   │   ├── ingest/           registry de normalizers, dedup, upsert, snapshots, alertas
│   │   ├── source/facebook/  FacebookNormalizer
│   │   ├── notify/           interface Notifier + implementação Discord
│   │   ├── workers/          registro, heartbeat, estados
│   │   └── httpapi/          net/http stdlib (padrões de rota do Go 1.22+)
│   ├── migrations/           goose (SQL)
│   └── queries/              sqlc + pgx v5
├── crawler/                  Python 3.13, uv
│   └── src/crawler/
│       ├── worker.py         loop: claim, run, submit
│       ├── api.py            cliente HTTP do Go
│       ├── session.py        contexto persistente, modo login
│       └── adapters/
│           ├── base.py       protocolo SourceAdapter
│           └── facebook/
│               ├── search.py     navegação e scroll
│               ├── capture.py    captura de respostas GraphQL
│               ├── fallback.py   JSON embutido no HTML
│               └── detect.py     checkpoint, login wall, layout changed
├── web/                      Next.js
└── docs/
    └── contracts/            JSON Schema de job e result
```

A imagem Docker do crawler usa Python 3.13 por compatibilidade conservadora de wheels.

### 2.5 Bibliotecas

- **Go:** pgx v5, sqlc, goose, `net/http` stdlib, go-co-op/gocron v2, `log/slog`, prometheus/client_golang, testcontainers-go.
- **Python:** playwright, httpx, structlog, pydantic (validação de contrato), pytest.
- **Web:** Next.js.

## 3. Modelo de dados (PostgreSQL 17)

Princípios:

- Preço sempre em `bigint` centavos, nunca float.
- O payload bruto é guardado para permitir re-normalização sem re-crawl.
- Snapshot só é gravado quando o conteúdo muda, não a cada vez que o anúncio é visto.

```sql
search_regions
  id text PK               -- ex.: 'curitiba-pr'
  name text
  kind text                -- 'city' | 'point' | 'state'
  city text, state char(2)
  lat double precision, lng double precision, radius_km int
  fb_location text NULL    -- slug ou ID numérico de cidade no Facebook
  enabled bool

search_definitions
  id uuid PK
  name text, keyword text
  enabled bool, priority smallint
  frequency interval, next_run_at timestamptz
  filters jsonb            -- {min_price, max_price, days_since_listed, condition}
  alert jsonb              -- {enabled, max_price, states[], on_new, on_price_drop, min_drop_pct}
  created_at, updated_at

search_definition_regions
  search_id uuid, region_id text, baseline_done bool DEFAULT false
  PK (search_id, region_id)

jobs
  id uuid PK
  type text                -- 'search' | 'enrich'
  source text              -- 'facebook_marketplace'
  search_id uuid, region_id text
  payload jsonb
  status text              -- 'queued' | 'running' | 'succeeded' | 'failed' | 'dead'
  priority smallint, attempts int, max_attempts int
  run_after timestamptz, lease_until timestamptz, worker_id text
  error_kind text, error_message text
  created_at, started_at, finished_at
  UNIQUE (search_id, region_id) WHERE status IN ('queued', 'running')

raw_captures               -- retenção de 14 dias
  id bigserial PK, job_id uuid, source text
  crawler_version text, parser_version text
  payload jsonb, captured_at timestamptz

listings
  id bigserial PK
  source text, external_id text
  url text, title text, description text
  price_cents bigint, currency char(3)
  location_text text, city text, state char(2)
  lat double precision, lng double precision
  image_url text
  seller_external_id text, seller_name text
  status text              -- 'active' | 'pending' | 'sold' | 'unseen'
  fingerprint text, content_hash text
  first_seen_at, last_seen_at, last_changed_at timestamptz
  raw_data jsonb           -- último raw
  created_at, updated_at
  UNIQUE (source, external_id)
  INDEX first_seen_at, last_seen_at, price_cents, (state, city), seller_external_id, fingerprint

listing_snapshots
  id bigserial PK, listing_id bigint
  price_cents bigint, title text, description text, status text
  job_id uuid, captured_at timestamptz, raw_data jsonb

search_listings
  search_id uuid, listing_id bigint
  first_seen_at, last_seen_at timestamptz
  PK (search_id, listing_id)

notifications              -- outbox idempotente
  id bigserial PK, listing_id bigint, search_id uuid
  kind text                -- 'new' | 'price_drop' | 'system'
  price_cents bigint, channel text
  created_at, sent_at timestamptz NULL, attempts int
  UNIQUE (listing_id, search_id, kind, price_cents)

workers
  id text PK, hostname text, status text
  crawler_version text, current_job_id uuid
  last_heartbeat_at timestamptz, jobs_ok int, jobs_failed int, cooldown_until timestamptz

source_sessions
  source text PK
  status text              -- 'ok' | 'needs_attention' | 'cooldown'
  last_checkpoint_at timestamptz, cooldown_until timestamptz, note text
```

Estados do worker: `starting`, `healthy`, `busy`, `idle`, `error`, `cooldown`, `stopped`.

### 3.1 Decisões do modelo

- **Deduplicação primária:** `UNIQUE (source, external_id)`. O ID do Facebook vem de `/marketplace/item/<id>/` (query string descartada).
- **Fingerprint:** `hash(normalize(title) + price + seller + location)`. É calculado e indexado, mas sem lógica de merge no MVP. Fica pronto para detectar repost e duplicatas multi-source.
- **`content_hash`:** `hash(title, price_cents, status, description)`. Determina se um novo snapshot é gravado.
- **Remoção:** ausência em uma busca não prova remoção, porque os resultados são paginados e limitados. O MVP marca `unseen` após `LISTING_UNSEEN_AFTER` (padrão 7 dias) sem aparecer. Confirmação de `sold` ou remoção vem do enrichment, ou de `is_sold`/`is_pending` quando a busca já trouxer esse dado.
- **PostGIS:** fora do MVP. `lat`/`lng` como `double precision`; PostGIS entra na fase geo.

## 4. Contrato Go ↔ Python

API interna em `/internal/v1`, autenticada por token compartilhado (`Authorization: Bearer $WORKER_TOKEN`).

```text
POST /internal/v1/workers/register       {worker_id, hostname, crawler_version, sources[]}
POST /internal/v1/workers/{id}/heartbeat {status, current_job_id}      a cada 15 s
POST /internal/v1/jobs/claim             {worker_id, sources[], types[]}
                                         → 200 job | 204 sem job disponível
POST /internal/v1/jobs/{id}/result       {worker_id, attempt, crawler_version, parser_version,
                                          items: [{external_id, raw}],
                                          stats: {pages, duration_ms}}
POST /internal/v1/jobs/{id}/fail         {worker_id, attempt, error_kind, message}
```

O claim concede lease de 10 minutos. Jobs com lease expirado voltam para `queued` (o attempt é contado). `result` e `fail` com `attempt` diferente do atual são ignorados (idempotência).

### 4.1 Job entregue ao worker

```json
{
  "job_id": "uuid",
  "attempt": 1,
  "type": "search",
  "source": "facebook_marketplace",
  "search": {
    "keyword": "Honda Civic EG",
    "filters": {"max_price": 30000, "days_since_listed": 7}
  },
  "region": {"id": "curitiba-pr", "fb_location": "curitiba", "lat": -25.43, "lng": -49.27},
  "limits": {"max_pages": 3, "max_items": 120},
  "stop_before": "2026-10-09T12:00:00Z"
}
```

`stop_before` é o início da última execução bem-sucedida da mesma busca e região. Na primeira execução é nulo.

### 4.2 Divisão de responsabilidade de parsing

- **Python** extrai o nó bruto (objeto `listing` do GraphQL ou o equivalente do JSON embutido) e o `external_id`.
- **Go** (`FacebookNormalizer`) mapeia o raw para o `Listing` canônico.
- Uma mudança de campo exige apenas um fix em Go e uma re-normalização de `raw_captures`, sem re-crawl.

Os schemas de job e result vivem em `docs/contracts/` como JSON Schema e são validados nos testes dos dois lados.

### 4.3 Fluxo do crawler por job de busca

1. Navega para `https://www.facebook.com/marketplace/{fb_location}/search?query=…&sortBy=creation_time_descend&minPrice=…&maxPrice=…&daysSinceListed=…`.
2. Detecção: URL em `/checkpoint`, `/login` ou `/two_step_verification`, ou modal de login visível. Se houver, reporta `fail` com o `error_kind` correspondente.
3. Captura: respostas de `/api/graphql/` identificadas pelo **formato** (`data.marketplace_search`), não por `doc_id`, que rotaciona. A primeira página também é lida do JSON embutido no HTML (`<script type="application/json">`).
4. Scroll lento com jitter de 2–6 s até `max_pages` ou `max_items`. Para antes se os itens ficarem mais antigos que `stop_before`.
5. Envia `result`. Zero resultados é sucesso, não erro.

Observação: com sessão logada, o raio de busca efetivo é o último escolhido na UI do Facebook (fica salvo no servidor); o parâmetro `radius` da URL é praticamente ignorado. O usuário deve ajustar o raio uma vez na UI.

### 4.4 Taxonomia de erro

| `error_kind` | Reação do Go |
|---|---|
| `network`, `timeout` | Retry com backoff de 1 min, 5 min e 25 min; depois `dead`. |
| `rate_limited` | Fonte em cooldown por 60 min. |
| `checkpoint`, `login_required` | `source_sessions.status = needs_attention`. Pausa toda a fonte. Alerta "login necessário" no Discord. Só volta após relogin e `POST /api/v1/sessions/{source}/resume`. |
| `layout_changed` | Nada capturado e fallback vazio. Uma nova tentativa; depois `dead`. Alerta no Discord. Screenshot e HTML salvos para debug. |

## 5. Ingest, deduplicação e alertas (Go)

Para cada item de um `result`, dentro de uma transação:

1. Grava o payload em `raw_captures`.
2. Normaliza com o normalizer da fonte.
3. Faz upsert em `listings` por `(source, external_id)` e atualiza `last_seen_at`.
4. Calcula `content_hash`. Se o anúncio é novo ou o hash mudou, grava em `listing_snapshots` e atualiza `last_changed_at`.
5. Se o preço caiu, marca o evento como `price_drop`.
6. Faz upsert em `search_listings`.
7. Avalia as regras de `alert` da busca e insere em `notifications` (outbox). O `UNIQUE` evita alertas duplicados.

Reprocessar o mesmo resultado duas vezes não cria anúncio, snapshot nem alerta duplicado.

**Baseline:** a primeira execução bem-sucedida de cada par (busca, região) apenas popula os dados e não gera alertas `new`. Ao final, `search_definition_regions.baseline_done = true`. Isso evita dezenas de mensagens no primeiro dia.

**Notifier:** uma goroutine lê `notifications` com `sent_at IS NULL`, envia ao Discord (embed com foto, título, preço, cidade e link) e marca `sent_at`. Falhas de envio são re-tentadas com backoff. Alertas de sistema (`checkpoint`, `layout_changed`) usam `kind = 'system'`.

## 6. Scheduler e rate budget

- Tick de 1 minuto.
- Definições habilitadas com `next_run_at <= now()` geram um job por região (`ON CONFLICT DO NOTHING` contra a constraint parcial de `jobs`).
- `next_run_at = now() + frequency ± jitter` (jitter de até 10% da frequência).
- O claim ordena por `priority DESC, run_after ASC`.
- O claim não entrega job de uma fonte quando:
  - a sessão da fonte não está `ok`;
  - a fonte está em cooldown;
  - o limite `FB_MAX_JOBS_PER_HOUR` (padrão 6) foi atingido;
  - o último job da fonte começou há menos de `FB_MIN_GAP` (padrão 4 min);
  - o horário está dentro das quiet hours (padrão 23h–7h, `America/Sao_Paulo`).

Todos os limites são configuráveis por variável de ambiente.

## 7. API pública e dashboard

API em `/api/v1`, exposta apenas em `127.0.0.1`, sem autenticação:

```text
GET/POST/PATCH/DELETE /searches         CRUD, enable/disable, regras de alerta
GET/POST/PATCH        /regions
GET                   /listings          filtros: search_id, state, city, min/max price, status, sort
GET                   /listings/{id}     detalhe + snapshots
GET                   /jobs              filtros: status, error_kind
GET                   /workers
GET                   /sessions
POST                  /sessions/{source}/resume
GET                   /stats
GET                   /healthz  /readyz  /metrics
```

Dashboard (Next.js), quatro telas:

- **Buscas:** CRUD, toggle de habilitação, configuração de alerta.
- **Anúncios:** tabela com miniatura, preço, cidade, "visto pela primeira vez" e filtros.
- **Detalhe do anúncio:** dados atuais e gráfico do histórico de preço.
- **Crawler:** estado do worker e da sessão, jobs por hora, taxa de sucesso, erros recentes, botão "liberar sessão".

## 8. Observabilidade

- **Logs:** Go com `log/slog` em JSON; Python com `structlog` em JSON. `job_id` e `worker_id` em todas as linhas relevantes.
- **Métricas** (`/metrics`, formato Prometheus, sem servidor Prometheus no MVP):
  - `jobs_total{source,type,status,error_kind}`
  - `job_duration_seconds{source,type}`
  - `listings_ingested_total{source,result="new|seen|changed"}`
  - `snapshots_total`, `notifications_total{kind,status}`
- **Debug de falhas:** em `layout_changed` e `checkpoint`, o crawler salva screenshot e HTML em um volume de debug (mantém os 50 mais recentes).
- O dashboard lê agregados de `/stats` (consultas no Postgres), não do `/metrics`.

## 9. Deploy (Docker Compose)

| Serviço | Imagem / build | Observações |
|---|---|---|
| `postgres` | `postgres:17` | Volume de dados. |
| `api` | build `backend/` | Migrations goose executadas no startup. |
| `crawler` | build `crawler/` | Python 3.13, Chromium, Xvfb, noVNC. Volumes de perfil e debug. `shm_size: 1gb`. |
| `web` | build `web/` | Next.js. |

- Todas as portas publicadas presas em `127.0.0.1`.
- Secrets em `.env` (gitignored), com `.env.example` commitado: `POSTGRES_PASSWORD`, `WORKER_TOKEN`, `DISCORD_WEBHOOK_URL`.
- Perfil do navegador, cookies e screenshots nunca entram no Git.

## 10. Testes

- **Go:**
  - `FacebookNormalizer` com fixtures JSON reais e sanitizadas.
  - Ingest, deduplicação, snapshots, baseline e alertas contra Postgres real (testcontainers-go).
  - Claim concorrente na fila (dois claimers, nenhum job entregue duas vezes).
  - Idempotência: o mesmo `result` enviado duas vezes não cria duplicatas.
  - Rate budget, quiet hours e pausa por sessão.
- **Python:**
  - Extração (`capture.py`, `fallback.py`) e detecção (`detect.py`) sobre fixtures HTML/JSON salvas.
  - Validação do contrato com pydantic contra os JSON Schemas.
  - Nenhum teste automatizado acessa o Facebook ao vivo.
- **Smoke manual:** `crawler run-once --search "<keyword>" --region curitiba-pr --dry-run` imprime os itens capturados sem enviar ao backend.

## 11. Critério de pronto do MVP

1. `docker compose up` sobe os quatro serviços.
2. Login manual via noVNC persiste entre reinícios do container.
3. Uma busca ("Honda Civic EG" × Curitiba/PR) roda automaticamente na frequência configurada.
4. Anúncios aparecem no dashboard sem duplicatas, com `first_seen_at` e `last_seen_at` corretos.
5. Uma mudança de preço gera snapshot, aparece no histórico e dispara alerta `price_drop` no Discord.
6. Um anúncio novo após o baseline dispara alerta `new` no Discord.
7. Um checkpoint simulado pausa a fonte e envia alerta de sistema.

## 12. Fora do escopo do MVP

Enrichment de detalhe, catálogo nacional de regiões, PostGIS, IA, outras fontes, RabbitMQ, Redis, múltiplos workers, multi-tenancy e autenticação da API.

## 13. Roadmap

Cada item vira uma spec e um plano próprios:

1. **MVP** (este documento).
2. **Regiões:** catálogo de cidades brasileiras (IBGE + `fb_location`), várias regiões por busca, prioridade e frequência adaptativa.
3. **Enrichment:** visita à página de detalhe de candidatos (descrição, fotos, vendedor, confirmação de `sold` ou remoção).
4. **Geo:** PostGIS, busca por raio, mapa.
5. **IA:** classificação e extração de atributos, fora do caminho crítico do crawler.
6. **Multi-source:** Mercado Livre (API oficial) e OLX, com adapters e normalizers independentes.
7. **Distribuição:** RabbitMQ e múltiplos workers, somente se surgir gargalo real.
