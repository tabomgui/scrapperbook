# Python Crawler Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Python worker that claims jobs from the Go backend's internal API, drives a persistent Playwright session against Facebook Marketplace, extracts raw listing data from GraphQL responses, and reports results (or classified failures) back — without ever touching business logic that belongs in Go.

**Architecture:** A small sync Python package. Pure, fixture-tested parsing functions (URL building, GraphQL/embedded-JSON extraction, checkpoint detection) are fully decoupled from the one Playwright-driving module that actually opens pages and scrolls. The worker loop (`claim → run adapter → submit result/fail`) is itself pure and tested with fake API/adapter objects, so it never needs a real browser or network to verify its control flow.

**Tech Stack:** Python 3.13 (Docker) / whatever `python3` is on the host for running the test suite (pure logic, no Playwright import required to run tests), `httpx` (calls the Go API), `pydantic` v2 (contracts + JSON Schema export), `pytest` + `respx` (HTTP mocking), Playwright (browser automation, Docker-only), Xvfb + x11vnc + noVNC (headed browser visible for manual login), Docker.

**Spec:** `docs/superpowers/specs/2026-10-09-marketplace-monitor-mvp-design.md`
**Depends on:** `docs/superpowers/plans/2026-10-09-backend-core.md` (plan 1, already implemented and verified — this plan is the worker that calls its `/internal/v1` API).

---

## Deviations from plan 1's tooling assumption

Plan 1's file tree sketch said "Python 3.13, uv". `uv` is not installed on this machine (`uv not found`). This plan uses a plain `venv` + `pip install -e .[dev]` workflow instead — standard, zero extra tooling dependency, and the test suite below never needs Playwright on the host (see "Why no Playwright on the host" below), so there's nothing uv would have bought here that plain pip doesn't.

## Why no Playwright on the host

Every automated test in this plan runs against **fixtures** — synthetic-but-realistic JSON/HTML saved to disk, not a live browser. Two reasons:

1. **The design explicitly forbids it.** Spec §10: "Nenhum teste automatizado acessa o Facebook ao vivo." The crawler's whole reason to exist is a *logged-in* session on the user's real account; there is no way to exercise that safely or repeatably in CI.
2. **It lets the whole suite run without Playwright installed on the host.** Only two files (`session.py`, `sources/facebook/run.py`) import `playwright.async_api`, and no test file imports either of them. The rest of the package — URL building, GraphQL parsing, embedded-JSON fallback, checkpoint detection, the API client, the worker loop — is plain, synchronous, fixture-driven Python.

The parts that genuinely need a live, logged-in browser (confirming Curitiba's real `fb_location`, capturing one real sanitized listing node, running an end-to-end smoke test against actual Facebook) are **Task 13, explicitly marked for the human user, not a subagent** — see that task for why.

## File structure

```text
crawler/
├── pyproject.toml
├── .dockerignore
├── Dockerfile
├── README.md
├── src/crawler/
│   ├── __init__.py
│   ├── errors.py                 ErrorKind, CrawlError
│   ├── contracts.py               pydantic models mirroring the Go API's JSON shapes
│   ├── config.py                  Settings.load(getenv) — same injected-getenv pattern as backend/internal/config
│   ├── api.py                     APIClient: register, heartbeat, claim, submit_result, submit_fail
│   ├── worker.py                  run_once(), run_forever()
│   └── sources/
│       ├── __init__.py
│       ├── base.py                Adapter protocol
│       └── facebook/
│           ├── __init__.py
│           ├── constants.py       SOURCE = "facebook_marketplace"
│           ├── url.py             build_search_url() — pure
│           ├── capture.py         parse_graphql_response() — pure
│           ├── fallback.py        parse_embedded_json() — pure
│           ├── detect.py          classify_url(), classify_status() — pure
│           ├── session.py         persistent-context helpers (is_logged_in is pure; launch is not)
│           └── run.py             FacebookAdapter — the only module that drives Playwright end to end
├── scripts/
│   └── login.py                   manual login entrypoint, run headed inside noVNC
└── tests/
    ├── conftest.py
    ├── test_errors.py
    ├── test_contracts.py
    ├── test_config.py
    ├── test_api.py
    ├── test_worker.py
    └── facebook/
        ├── test_url.py
        ├── test_capture.py
        ├── test_fallback.py
        ├── test_detect.py
        └── test_session.py        (only the pure is_logged_in helper)

docs/contracts/
├── job.schema.json                 generated from contracts.ClaimedJob
└── result.schema.json              generated from contracts.ResultRequest
```

Prerequisites on the machine running these tasks: `python3` (3.11+; whatever is on the host is fine), Docker running (needed only for Task 12's build/boot check — not for Tasks 1-11).

---

### Task 1: Scaffold the crawler package

**Files:**
- Create: `crawler/pyproject.toml`, `crawler/.dockerignore`, `crawler/README.md`
- Create: `crawler/src/crawler/__init__.py`
- Test: `crawler/tests/test_smoke.py`

- [ ] **Step 1: Criar `crawler/pyproject.toml`**

```toml
[project]
name = "crawler"
version = "0.1.0"
requires-python = ">=3.11"
dependencies = [
    "httpx>=0.27,<1",
    "pydantic>=2,<3",
]

[project.optional-dependencies]
dev = [
    "pytest>=8,<9",
    "respx>=0.21,<1",
]

[tool.pytest.ini_options]
pythonpath = ["src"]
testpaths = ["tests"]

[build-system]
requires = ["setuptools>=68"]
build-backend = "setuptools.build_meta"

[tool.setuptools.packages.find]
where = ["src"]
```

- [ ] **Step 2: Criar `crawler/.dockerignore`**

```text
.venv/
__pycache__/
**/__pycache__/
*.pyc
.pytest_cache/
tests/
```

- [ ] **Step 3: Criar `crawler/README.md`**

```markdown
# crawler

Python worker for the marketplace monitor's Go backend (`../backend`).

## Dev setup

    cd crawler
    python3 -m venv .venv
    source .venv/bin/activate
    pip install -e .[dev]

## Tests

    pytest -v

No test here touches a real browser or Facebook — see the design spec's
"Why no Playwright on the host" note in the implementation plan.
```

- [ ] **Step 4: Criar o pacote vazio**

`crawler/src/crawler/__init__.py`:

```python
"""Facebook Marketplace crawler worker."""
```

- [ ] **Step 5: Escrever um teste trivial e confirmar que falha**

`crawler/tests/test_smoke.py`:

```python
import crawler


def test_package_importable():
    assert crawler.__doc__
```

Run: `cd crawler && python3 -m venv .venv && .venv/bin/pip install -e '.[dev]' -q && .venv/bin/pytest -v`
Expected (before Step 4 exists, if run out of order): `ModuleNotFoundError: No module named 'crawler'`. Since Step 4 already created `__init__.py` above, this should instead PASS immediately — confirm it does.

- [ ] **Step 6: Confirmar que passa**

Run: `cd crawler && .venv/bin/pytest -v`
Expected: `test_smoke.py::test_package_importable PASSED`

- [ ] **Step 7: Commit**

```bash
git add crawler/
git commit -m "chore: scaffold crawler package"
```

---

### Task 2: Error taxonomy

The six kinds here must match `backend/internal/jobs/lifecycle.go`'s `ErrorKind` constants exactly (`network`, `timeout`, `rate_limited`, `checkpoint`, `login_required`, `layout_changed`) — the Go side's `ParseErrorKind` rejects anything else with a 400.

**Files:**
- Create: `crawler/src/crawler/errors.py`
- Test: `crawler/tests/test_errors.py`

- [ ] **Step 1: Escrever o teste que falha**

`crawler/tests/test_errors.py`:

```python
import pytest

from crawler.errors import CrawlError, ErrorKind


def test_error_kind_values_match_go():
    assert {k.value for k in ErrorKind} == {
        "network", "timeout", "rate_limited",
        "checkpoint", "login_required", "layout_changed",
    }


def test_crawl_error_carries_kind_and_message():
    err = CrawlError(ErrorKind.CHECKPOINT, "hit a checkpoint page")
    assert err.kind is ErrorKind.CHECKPOINT
    assert str(err) == "hit a checkpoint page"


def test_crawl_error_requires_a_valid_kind():
    with pytest.raises(TypeError):
        CrawlError("not-a-kind", "x")
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd crawler && .venv/bin/pytest tests/test_errors.py -v`
Expected: `ModuleNotFoundError: No module named 'crawler.errors'`

- [ ] **Step 3: Implementar**

`crawler/src/crawler/errors.py`:

```python
"""Error classification shared with the Go backend's jobs.ErrorKind."""

from enum import Enum


class ErrorKind(str, Enum):
    NETWORK = "network"
    TIMEOUT = "timeout"
    RATE_LIMITED = "rate_limited"
    CHECKPOINT = "checkpoint"
    LOGIN_REQUIRED = "login_required"
    LAYOUT_CHANGED = "layout_changed"


class CrawlError(Exception):
    """Raised by an adapter to report a classified failure for one job.

    An unclassified exception from an adapter is treated by the worker loop
    as ErrorKind.NETWORK — adapters should only raise CrawlError directly
    when they can tell *which* kind of failure this actually was.
    """

    def __init__(self, kind: ErrorKind, message: str):
        if not isinstance(kind, ErrorKind):
            raise TypeError(f"kind must be an ErrorKind, got {type(kind)!r}")
        super().__init__(message)
        self.kind = kind
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd crawler && .venv/bin/pytest tests/test_errors.py -v`
Expected: 3 testes PASS.

- [ ] **Step 5: Commit**

```bash
git add crawler/src/crawler/errors.py crawler/tests/test_errors.py
git commit -m "feat(crawler): add error taxonomy matching the Go backend"
```

---

### Task 3: Contracts (pydantic models + JSON Schema export)

These mirror the exact JSON shapes `backend/internal/httpapi/internal.go` sends and expects (plan 1, Task 10). Field names are snake_case on both sides already — no translation layer needed.

**Files:**
- Create: `crawler/src/crawler/contracts.py`
- Create: `crawler/scripts/export_contracts.py`
- Test: `crawler/tests/test_contracts.py`

- [ ] **Step 1: Escrever os testes que falham**

`crawler/tests/test_contracts.py`:

```python
import json
from pathlib import Path

from crawler.contracts import (
    ClaimedJob,
    FailRequest,
    Outcome,
    RegionPayload,
    Registration,
    ResultItem,
    ResultRequest,
    SearchPayload,
    Stats,
)

REPO_ROOT = Path(__file__).resolve().parents[2]
CONTRACTS_DIR = REPO_ROOT / "docs" / "contracts"


def test_claimed_job_parses_the_go_claim_response():
    raw = {
        "job_id": "8548d034-1911-4e59-a899-3afe5c67e8aa",
        "attempt": 1,
        "type": "search",
        "source": "facebook_marketplace",
        "stop_before": None,
        "search": {"id": "00000000-0000-0000-0000-00000000c1c1",
                    "keyword": "Honda Civic EG", "filters": {"max_price": 30000}},
        "region": {"id": "curitiba-pr", "fb_location": "curitiba",
                    "lat": -25.4284, "lng": -49.2733, "radius_km": 60},
        "limits": {"max_pages": 3, "max_items": 120},
    }
    job = ClaimedJob.model_validate(raw)
    assert job.job_id == raw["job_id"]
    assert job.attempt == 1
    assert job.stop_before is None
    assert job.search.keyword == "Honda Civic EG"
    assert job.region.fb_location == "curitiba"
    assert job.limits.max_pages == 3


def test_claimed_job_parses_a_stop_before_timestamp():
    raw = {
        "job_id": "x", "attempt": 2, "type": "search", "source": "facebook_marketplace",
        "stop_before": "2026-10-09T15:00:00Z",
        "search": {"id": "s", "keyword": "k", "filters": {}},
        "region": {"id": "r", "fb_location": "curitiba", "lat": None, "lng": None, "radius_km": None},
        "limits": {"max_pages": 3, "max_items": 120},
    }
    job = ClaimedJob.model_validate(raw)
    assert job.stop_before is not None
    assert job.stop_before.year == 2026


def test_result_request_serializes_items_as_sent_to_go():
    req = ResultRequest(
        worker_id="w1", attempt=1, crawler_version="0.1.0", parser_version="fb-1",
        items=[ResultItem(external_id="1", raw={"id": "1", "marketplace_listing_title": "Civic"})],
        stats={"pages": 1, "duration_ms": 1200},
    )
    body = req.model_dump(mode="json")
    assert body["items"][0]["external_id"] == "1"
    assert body["items"][0]["raw"]["marketplace_listing_title"] == "Civic"
    assert body["stats"] == {"pages": 1, "duration_ms": 1200}


def test_fail_request_and_outcome_and_registration_round_trip():
    fail = FailRequest(worker_id="w1", attempt=1, error_kind="network", message="boom")
    assert fail.model_dump()["error_kind"] == "network"

    outcome = Outcome.model_validate({"status": "queued", "run_after": "2026-10-09T16:00:00Z"})
    assert outcome.status == "queued"
    assert outcome.run_after is not None

    reg = Registration(worker_id="w1", hostname="box", crawler_version="0.1.0",
                        sources=["facebook_marketplace"])
    assert reg.model_dump()["sources"] == ["facebook_marketplace"]


def test_stats_parses_the_go_ingest_response():
    stats = Stats.model_validate({"new": 2, "changed": 0, "seen": 0, "invalid": 0, "alerts": 1})
    assert stats.new == 2 and stats.alerts == 1


def test_checked_in_json_schemas_match_the_models():
    job_schema = json.loads((CONTRACTS_DIR / "job.schema.json").read_text())
    result_schema = json.loads((CONTRACTS_DIR / "result.schema.json").read_text())
    assert job_schema == ClaimedJob.model_json_schema()
    assert result_schema == ResultRequest.model_json_schema()
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd crawler && .venv/bin/pytest tests/test_contracts.py -v`
Expected: `ModuleNotFoundError: No module named 'crawler.contracts'`

- [ ] **Step 3: Implementar os modelos**

`crawler/src/crawler/contracts.py`:

```python
"""Pydantic models mirroring the Go backend's /internal/v1 JSON shapes.

Keep field names identical to backend/internal/httpapi/internal.go's
json tags — there is no translation layer on purpose.
"""

from datetime import datetime
from typing import Any

from pydantic import BaseModel


class SearchPayload(BaseModel):
    id: str
    keyword: str
    filters: dict[str, Any] = {}


class RegionPayload(BaseModel):
    id: str
    fb_location: str
    lat: float | None = None
    lng: float | None = None
    radius_km: int | None = None


class PageLimits(BaseModel):
    max_pages: int
    max_items: int


class ClaimedJob(BaseModel):
    """The flattened JSON object POST /internal/v1/jobs/claim returns."""

    job_id: str
    attempt: int
    type: str
    source: str
    stop_before: datetime | None = None
    search: SearchPayload
    region: RegionPayload
    limits: PageLimits


class ResultItem(BaseModel):
    external_id: str
    raw: dict[str, Any]


class ResultRequest(BaseModel):
    """Body of POST /internal/v1/jobs/{id}/result."""

    worker_id: str
    attempt: int
    crawler_version: str
    parser_version: str
    items: list[ResultItem]
    stats: dict[str, Any] = {}


class Stats(BaseModel):
    """The ingest.Stats JSON the result endpoint returns on success."""

    new: int
    changed: int
    seen: int
    invalid: int
    alerts: int


class FailRequest(BaseModel):
    """Body of POST /internal/v1/jobs/{id}/fail."""

    worker_id: str
    attempt: int
    error_kind: str
    message: str = ""


class Outcome(BaseModel):
    """The jobs.Outcome JSON the fail endpoint returns."""

    status: str
    run_after: datetime | None = None


class Registration(BaseModel):
    """Body of POST /internal/v1/workers/register."""

    worker_id: str
    hostname: str
    crawler_version: str
    sources: list[str] = []


class HeartbeatRequest(BaseModel):
    """Body of POST /internal/v1/workers/{id}/heartbeat."""

    status: str
    current_job_id: str | None = None
```

- [ ] **Step 4: Implementar o script de exportação e gerar os schemas**

`crawler/scripts/export_contracts.py`:

```python
"""Regenerate docs/contracts/*.schema.json from the pydantic models.

Run after changing crawler/src/crawler/contracts.py:
    cd crawler && .venv/bin/python scripts/export_contracts.py
"""

import json
from pathlib import Path

from crawler.contracts import ClaimedJob, ResultRequest

REPO_ROOT = Path(__file__).resolve().parents[2]
CONTRACTS_DIR = REPO_ROOT / "docs" / "contracts"


def main() -> None:
    CONTRACTS_DIR.mkdir(parents=True, exist_ok=True)
    (CONTRACTS_DIR / "job.schema.json").write_text(
        json.dumps(ClaimedJob.model_json_schema(), indent=2) + "\n")
    (CONTRACTS_DIR / "result.schema.json").write_text(
        json.dumps(ResultRequest.model_json_schema(), indent=2) + "\n")
    print(f"wrote job.schema.json and result.schema.json to {CONTRACTS_DIR}")


if __name__ == "__main__":
    main()
```

Run: `cd crawler && .venv/bin/python scripts/export_contracts.py`
Expected: `wrote job.schema.json and result.schema.json to .../docs/contracts`

- [ ] **Step 5: Rodar e confirmar que passa**

Run: `cd crawler && .venv/bin/pytest tests/test_contracts.py -v`
Expected: 6 testes PASS (the schema-sync test now has files to compare against, from Step 4).

- [ ] **Step 6: Commit**

```bash
git add crawler/src/crawler/contracts.py crawler/scripts/export_contracts.py \
  crawler/tests/test_contracts.py docs/contracts/
git commit -m "feat(crawler): add pydantic contracts and exported JSON schemas"
```

---

### Task 4: API client

Wraps every `/internal/v1` call behind one class. `claim()` returns `None` on HTTP 204 (no job available) — this is the one non-error "nothing happened" path; everything else is either a parsed model or a raised exception.

**Files:**
- Create: `crawler/src/crawler/api.py`
- Test: `crawler/tests/test_api.py`

- [ ] **Step 1: Escrever os testes que falham**

`crawler/tests/test_api.py`:

```python
import json

import httpx
import pytest
import respx

from crawler.api import APIClient, APIError
from crawler.contracts import ResultItem

BASE = "http://api.test"
TOKEN = "secret"


@pytest.fixture
def client():
    return APIClient(base_url=BASE, token=TOKEN, worker_id="w1")


@respx.mock
def test_register_sends_bearer_token_and_body(client):
    route = respx.post(f"{BASE}/internal/v1/workers/register").mock(
        return_value=httpx.Response(204))
    client.register(hostname="box", crawler_version="0.1.0", sources=["facebook_marketplace"])
    req = route.calls.last.request
    assert req.headers["authorization"] == f"Bearer {TOKEN}"
    # httpx.Request has no .json() (only httpx.Response does) — decode the
    # raw body instead.
    assert json.loads(req.content) == {"worker_id": "w1", "hostname": "box",
                                        "crawler_version": "0.1.0", "sources": ["facebook_marketplace"]}


@respx.mock
def test_heartbeat_posts_to_the_right_path(client):
    respx.post(f"{BASE}/internal/v1/workers/w1/heartbeat").mock(return_value=httpx.Response(204))
    client.heartbeat(status="idle")  # must not raise


@respx.mock
def test_heartbeat_sends_current_job_id_when_given(client):
    route = respx.post(f"{BASE}/internal/v1/workers/w1/heartbeat").mock(return_value=httpx.Response(204))
    client.heartbeat(status="busy", current_job_id="j1")
    assert json.loads(route.calls.last.request.content) == {"status": "busy", "current_job_id": "j1"}


@respx.mock
def test_claim_returns_none_on_204(client):
    respx.post(f"{BASE}/internal/v1/jobs/claim").mock(return_value=httpx.Response(204))
    assert client.claim(sources=["facebook_marketplace"], types=["search"]) is None


@respx.mock
def test_claim_parses_a_job_on_200(client):
    body = {
        "job_id": "j1", "attempt": 1, "type": "search", "source": "facebook_marketplace",
        "stop_before": None,
        "search": {"id": "s1", "keyword": "Honda Civic EG", "filters": {}},
        "region": {"id": "curitiba-pr", "fb_location": "curitiba",
                    "lat": None, "lng": None, "radius_km": None},
        "limits": {"max_pages": 3, "max_items": 120},
    }
    respx.post(f"{BASE}/internal/v1/jobs/claim").mock(return_value=httpx.Response(200, json=body))
    job = client.claim(sources=["facebook_marketplace"], types=["search"])
    assert job.job_id == "j1"
    assert job.search.keyword == "Honda Civic EG"


@respx.mock
def test_submit_result_returns_stats(client):
    respx.post(f"{BASE}/internal/v1/jobs/j1/result").mock(
        return_value=httpx.Response(200, json={"new": 1, "changed": 0, "seen": 0, "invalid": 0, "alerts": 0}))
    stats = client.submit_result(
        job_id="j1", attempt=1, crawler_version="0.1.0", parser_version="fb-1",
        items=[ResultItem(external_id="1", raw={"id": "1"})], stats={"pages": 1, "duration_ms": 10})
    assert stats.new == 1


@respx.mock
def test_submit_result_raises_api_error_on_409(client):
    respx.post(f"{BASE}/internal/v1/jobs/j1/result").mock(
        return_value=httpx.Response(409, json={"error": "job is not running with this attempt"}))
    with pytest.raises(APIError) as exc_info:
        client.submit_result(job_id="j1", attempt=1, crawler_version="0.1.0",
                              parser_version="fb-1", items=[], stats={})
    assert exc_info.value.status_code == 409


@respx.mock
def test_submit_fail_returns_outcome(client):
    respx.post(f"{BASE}/internal/v1/jobs/j1/fail").mock(
        return_value=httpx.Response(200, json={"status": "queued", "run_after": "2026-10-09T16:00:00Z"}))
    outcome = client.submit_fail(job_id="j1", attempt=1, error_kind="network", message="boom")
    assert outcome.status == "queued"


@respx.mock
def test_unauthorized_raises_api_error(client):
    respx.post(f"{BASE}/internal/v1/jobs/claim").mock(return_value=httpx.Response(401))
    with pytest.raises(APIError) as exc_info:
        client.claim(sources=["facebook_marketplace"], types=["search"])
    assert exc_info.value.status_code == 401
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd crawler && .venv/bin/pytest tests/test_api.py -v`
Expected: `ModuleNotFoundError: No module named 'crawler.api'`

- [ ] **Step 3: Implementar**

`crawler/src/crawler/api.py`:

```python
"""HTTP client for the Go backend's /internal/v1 API."""

from typing import Any

import httpx

from crawler.contracts import (
    ClaimedJob,
    FailRequest,
    HeartbeatRequest,
    Outcome,
    Registration,
    ResultItem,
    ResultRequest,
    Stats,
)


class APIError(Exception):
    """Raised for any non-2xx response that isn't the documented 204-empty-claim case."""

    def __init__(self, status_code: int, body: Any):
        super().__init__(f"API error {status_code}: {body}")
        self.status_code = status_code
        self.body = body


class APIClient:
    def __init__(self, base_url: str, token: str, worker_id: str, timeout: float = 30.0):
        self.worker_id = worker_id
        self._http = httpx.Client(
            base_url=base_url,
            headers={"Authorization": f"Bearer {token}"},
            timeout=timeout,
        )

    def close(self) -> None:
        self._http.close()

    def _post(self, path: str, json: dict) -> httpx.Response:
        resp = self._http.post(path, json=json)
        if resp.status_code >= 400:
            try:
                body = resp.json()
            except ValueError:
                body = resp.text
            raise APIError(resp.status_code, body)
        return resp

    def register(self, hostname: str, crawler_version: str, sources: list[str]) -> None:
        reg = Registration(worker_id=self.worker_id, hostname=hostname,
                            crawler_version=crawler_version, sources=sources)
        self._post("/internal/v1/workers/register", reg.model_dump())

    def heartbeat(self, status: str, current_job_id: str | None = None) -> None:
        req = HeartbeatRequest(status=status, current_job_id=current_job_id)
        self._post(f"/internal/v1/workers/{self.worker_id}/heartbeat", req.model_dump())

    def claim(self, sources: list[str], types: list[str]) -> ClaimedJob | None:
        resp = self._post("/internal/v1/jobs/claim",
                           {"worker_id": self.worker_id, "sources": sources, "types": types})
        if resp.status_code == 204:
            return None
        return ClaimedJob.model_validate(resp.json())

    def submit_result(self, job_id: str, attempt: int, crawler_version: str, parser_version: str,
                       items: list[ResultItem], stats: dict) -> Stats:
        req = ResultRequest(worker_id=self.worker_id, attempt=attempt,
                             crawler_version=crawler_version, parser_version=parser_version,
                             items=items, stats=stats)
        resp = self._post(f"/internal/v1/jobs/{job_id}/result", req.model_dump(mode="json"))
        return Stats.model_validate(resp.json())

    def submit_fail(self, job_id: str, attempt: int, error_kind: str, message: str) -> Outcome:
        req = FailRequest(worker_id=self.worker_id, attempt=attempt,
                           error_kind=error_kind, message=message)
        resp = self._post(f"/internal/v1/jobs/{job_id}/fail", req.model_dump())
        return Outcome.model_validate(resp.json())
```

Note: `claim`'s `_post` call treats 204 specially by checking `resp.status_code` *before* `_post` would raise — but `_post` only raises on `>= 400`, so 204 flows through normally. Re-read `claim()` above: it calls `self._post(...)` (which does NOT raise for 204, only for >=400), then checks `if resp.status_code == 204`. This is correct as written.

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd crawler && .venv/bin/pytest tests/test_api.py -v`
Expected: 9 testes PASS.

- [ ] **Step 5: Commit**

```bash
git add crawler/src/crawler/api.py crawler/tests/test_api.py
git commit -m "feat(crawler): add HTTP client for the internal API"
```

---

### Task 5: Facebook search URL builder

**Files:**
- Create: `crawler/src/crawler/sources/__init__.py`, `crawler/src/crawler/sources/base.py`
- Create: `crawler/src/crawler/sources/facebook/__init__.py`, `crawler/src/crawler/sources/facebook/constants.py`, `crawler/src/crawler/sources/facebook/url.py`
- Test: `crawler/tests/facebook/__init__.py`, `crawler/tests/facebook/test_url.py`

- [ ] **Step 1: Criar os pacotes vazios**

`crawler/src/crawler/sources/__init__.py`:
```python
```

`crawler/src/crawler/sources/facebook/__init__.py`:
```python
```

`crawler/tests/facebook/__init__.py`:
```python
```

- [ ] **Step 2: Criar o protocolo `Adapter`**

`crawler/src/crawler/sources/base.py`:

```python
"""The interface every source adapter implements."""

from typing import Protocol

from crawler.contracts import ClaimedJob, ResultItem


class Adapter(Protocol):
    source: str

    def run(self, job: ClaimedJob) -> list[ResultItem]:
        """Execute one job and return the raw items found.

        An empty list means zero results, which is success, not failure
        (see design spec §4.4: "0 resultados não é erro"). Raise
        crawler.errors.CrawlError for a classified failure, or let any
        other exception propagate — the worker loop treats an
        unclassified exception as ErrorKind.NETWORK.
        """
        ...
```

- [ ] **Step 3: Escrever o teste que falha**

`crawler/tests/facebook/test_url.py`:

```python
from crawler.sources.facebook.url import build_search_url


def test_basic_search_url():
    url = build_search_url(fb_location="curitiba", keyword="Honda Civic EG", filters={})
    assert url == (
        "https://www.facebook.com/marketplace/curitiba/search"
        "?query=Honda+Civic+EG&sortBy=creation_time_descend"
    )


def test_search_url_includes_price_and_days_filters():
    url = build_search_url(
        fb_location="curitiba", keyword="Honda Civic EG",
        filters={"max_price": 30000, "min_price": 10000, "days_since_listed": 7},
    )
    assert "maxPrice=30000" in url
    assert "minPrice=10000" in url
    assert "daysSinceListed=7" in url


def test_search_url_ignores_unknown_filter_keys():
    url = build_search_url(fb_location="curitiba", keyword="x", filters={"condition": "used"})
    assert "condition" not in url
```

- [ ] **Step 4: Rodar e confirmar falha**

Run: `cd crawler && .venv/bin/pytest tests/facebook/test_url.py -v`
Expected: `ModuleNotFoundError: No module named 'crawler.sources'`

- [ ] **Step 5: Implementar**

`crawler/src/crawler/sources/facebook/constants.py`:

```python
SOURCE = "facebook_marketplace"
```

`crawler/src/crawler/sources/facebook/url.py`:

```python
"""Builds the Facebook Marketplace search URL from a job's search+region payload.

Only max_price/min_price/days_since_listed are forwarded today — the design
spec notes the radius URL param is unreliable while logged in (the server
uses whatever radius was last set in the UI instead), so it's deliberately
not sent here; see design spec §4.3.
"""

from urllib.parse import urlencode

BASE = "https://www.facebook.com/marketplace"

_FILTER_PARAM_NAMES = {
    "max_price": "maxPrice",
    "min_price": "minPrice",
    "days_since_listed": "daysSinceListed",
}


def build_search_url(fb_location: str, keyword: str, filters: dict) -> str:
    params = {"query": keyword, "sortBy": "creation_time_descend"}
    for key, param_name in _FILTER_PARAM_NAMES.items():
        if filters.get(key) is not None:
            params[param_name] = str(filters[key])
    return f"{BASE}/{fb_location}/search?{urlencode(params)}"
```

- [ ] **Step 6: Rodar e confirmar que passa**

Run: `cd crawler && .venv/bin/pytest tests/facebook/test_url.py -v`
Expected: 3 testes PASS.

- [ ] **Step 7: Commit**

```bash
git add crawler/src/crawler/sources/ crawler/tests/facebook/__init__.py crawler/tests/facebook/test_url.py
git commit -m "feat(crawler): add adapter protocol and facebook search url builder"
```

---

### Task 6: GraphQL response parser

The fixture here is **synthetic**, built from the node shape documented by the open source projects researched during brainstorming (`secondhand-mcp`, `facebook-marketplace-mcp`) — the same caveat as plan 1's Facebook normalizer fixture. Task 13 (manual, human-only) replaces it with one real, sanitized capture.

**Files:**
- Create: `crawler/src/crawler/sources/facebook/capture.py`
- Create: `crawler/testdata/graphql_search_response.json`
- Test: `crawler/tests/facebook/test_capture.py`

- [ ] **Step 1: Criar a fixture**

`crawler/testdata/graphql_search_response.json`:

```json
{
  "data": {
    "marketplace_search": {
      "feed_units": {
        "edges": [
          {
            "node": {
              "listing": {
                "id": "1029384756",
                "marketplace_listing_title": "Honda Civic EG 1993 hatch",
                "listing_price": {"amount": "23500.00", "currency": "BRL"},
                "primary_listing_photo": {"image": {"uri": "https://scontent.example/civic.jpg"}},
                "location": {"reverse_geocode": {"city": "Curitiba", "state": "PR"}},
                "marketplace_listing_seller": {"id": "555", "name": "Vendedor Teste"},
                "is_sold": false,
                "is_pending": false
              }
            },
            "cursor": "abc123"
          },
          {
            "node": {
              "listing": {
                "id": "1029384757",
                "marketplace_listing_title": "Honda Civic EG5 1993",
                "listing_price": {"amount": "21000.00", "currency": "BRL"},
                "location": {"reverse_geocode": {"city": "Curitiba", "state": "PR"}},
                "is_sold": false,
                "is_pending": false
              }
            },
            "cursor": "def456"
          },
          {
            "node": {"typename_marker": "some_non_listing_unit"}
          }
        ]
      }
    }
  }
}
```

- [ ] **Step 2: Escrever os testes que falham**

`crawler/tests/facebook/test_capture.py`:

```python
import json
from pathlib import Path

from crawler.sources.facebook.capture import parse_graphql_response

FIXTURE = json.loads(
    (Path(__file__).resolve().parents[2] / "testdata" / "graphql_search_response.json").read_text()
)


def test_parses_two_listings_and_skips_the_non_listing_edge():
    items = parse_graphql_response(FIXTURE)
    assert items is not None
    assert len(items) == 2
    assert items[0]["external_id"] == "1029384756"
    assert items[0]["raw"]["marketplace_listing_title"] == "Honda Civic EG 1993 hatch"
    assert items[1]["external_id"] == "1029384757"


def test_returns_none_for_an_unrelated_graphql_response():
    assert parse_graphql_response({"data": {"viewer": {}}}) is None
    assert parse_graphql_response({}) is None
    assert parse_graphql_response({"data": None}) is None


def test_returns_empty_list_for_zero_results():
    empty = {"data": {"marketplace_search": {"feed_units": {"edges": []}}}}
    assert parse_graphql_response(empty) == []
```

- [ ] **Step 3: Rodar e confirmar falha**

Run: `cd crawler && .venv/bin/pytest tests/facebook/test_capture.py -v`
Expected: `ModuleNotFoundError: No module named 'crawler.sources.facebook.capture'`

- [ ] **Step 4: Implementar**

`crawler/src/crawler/sources/facebook/capture.py`:

```python
"""Extracts listing nodes from a Facebook Marketplace search GraphQL response.

The response is matched by *shape* (data.marketplace_search.feed_units.edges),
not by doc_id — doc_ids rotate when Facebook deploys, the shape is more
stable (see design spec §4.3/§19).
"""

from typing import Any


def parse_graphql_response(body: dict) -> list[dict[str, Any]] | None:
    """Returns a list of {"external_id", "raw"} dicts, or None if `body`
    isn't a marketplace search response at all (wrong shape — the caller
    should try the next extraction strategy, e.g. the embedded-JSON
    fallback). An empty list means a real response with zero listings.
    """
    try:
        edges = body["data"]["marketplace_search"]["feed_units"]["edges"]
    except (KeyError, TypeError):
        return None
    if not isinstance(edges, list):
        return None

    items: list[dict[str, Any]] = []
    for edge in edges:
        if not isinstance(edge, dict):
            continue
        listing = edge.get("node", {}).get("listing")
        if not isinstance(listing, dict) or "id" not in listing:
            continue
        items.append({"external_id": str(listing["id"]), "raw": listing})
    return items
```

- [ ] **Step 5: Rodar e confirmar que passa**

Run: `cd crawler && .venv/bin/pytest tests/facebook/test_capture.py -v`
Expected: 3 testes PASS.

- [ ] **Step 6: Commit**

```bash
git add crawler/src/crawler/sources/facebook/capture.py crawler/testdata/ crawler/tests/facebook/test_capture.py
git commit -m "feat(crawler): parse facebook marketplace graphql search responses"
```

---

### Task 7: Embedded-JSON fallback parser

Used for the first page of results, which Facebook also embeds directly in the HTML (so the crawler doesn't have to wait for an XHR on initial load). Same synthetic-fixture caveat as Task 6.

**Files:**
- Create: `crawler/src/crawler/sources/facebook/fallback.py`
- Create: `crawler/testdata/embedded_page.html`
- Test: `crawler/tests/facebook/test_fallback.py`

- [ ] **Step 1: Criar a fixture**

`crawler/testdata/embedded_page.html`:

```html
<!DOCTYPE html>
<html>
<head><title>Marketplace</title></head>
<body>
<div id="app"></div>
<script type="text/javascript">window.__unrelated = {"foo": "bar"};</script>
<script type="application/json" data-sjs>
{"require": [["some", "unrelated", "blob", {"count": 3}]]}
</script>
<script type="application/json" data-sjs>
{
  "require": [[
    "RelayPrefetchedStreamCache", "push", [{
      "__bbox": {
        "result": {
          "data": {
            "marketplace_search": {
              "feed_units": {
                "edges": [
                  {"node": {"listing": {
                    "id": "1029384756",
                    "marketplace_listing_title": "Honda Civic EG 1993 hatch",
                    "listing_price": {"amount": "23500.00", "currency": "BRL"},
                    "location": {"reverse_geocode": {"city": "Curitiba", "state": "PR"}},
                    "is_sold": false, "is_pending": false
                  }}}
                ]
              }
            }
          }
        }
      }
    }]
  ]]
}
</script>
</body>
</html>
```

- [ ] **Step 2: Escrever os testes que falham**

`crawler/tests/facebook/test_fallback.py`:

```python
from pathlib import Path

from crawler.sources.facebook.fallback import parse_embedded_json

FIXTURE_HTML = (
    Path(__file__).resolve().parents[2] / "testdata" / "embedded_page.html"
).read_text()


def test_finds_the_listing_buried_in_an_unrelated_json_blob():
    items = parse_embedded_json(FIXTURE_HTML)
    assert len(items) == 1
    assert items[0]["external_id"] == "1029384756"
    assert items[0]["raw"]["marketplace_listing_title"] == "Honda Civic EG 1993 hatch"


def test_returns_empty_list_when_nothing_matches():
    assert parse_embedded_json("<html><body>no scripts here</body></html>") == []
    assert parse_embedded_json(
        '<script type="application/json">{"not": "json-that-matters"}</script>'
    ) == []


def test_tolerates_malformed_json_in_one_script_tag():
    html = (
        '<script type="application/json">{not valid json</script>'
        '<script type="application/json">'
        '{"data":{"marketplace_search":{"feed_units":{"edges":'
        '[{"node":{"listing":{"id":"1","marketplace_listing_title":"Civic"}}}]}}}}'
        "</script>"
    )
    items = parse_embedded_json(html)
    assert len(items) == 1
    assert items[0]["external_id"] == "1"
```

- [ ] **Step 3: Rodar e confirmar falha**

Run: `cd crawler && .venv/bin/pytest tests/facebook/test_fallback.py -v`
Expected: `ModuleNotFoundError: No module named 'crawler.sources.facebook.fallback'`

- [ ] **Step 4: Implementar**

`crawler/src/crawler/sources/facebook/fallback.py`:

```python
"""Falls back to the JSON Facebook embeds directly in the search page's
HTML, for when no GraphQL response was captured (e.g. the first page of
results, server-rendered before any XHR fires).
"""

import json
import re
from typing import Any

from crawler.sources.facebook.capture import parse_graphql_response

_SCRIPT_RE = re.compile(
    r'<script[^>]*type="application/json"[^>]*>(.*?)</script>', re.DOTALL
)


def parse_embedded_json(html: str) -> list[dict[str, Any]]:
    """Scans every `<script type="application/json">` tag for one that,
    anywhere in its structure, contains a "marketplace_search" key — the
    page embeds this inside a deeply nested, version-specific wrapper that
    isn't worth modeling exactly, so this searches for the key by shape
    instead of by path.
    """
    for match in _SCRIPT_RE.finditer(html):
        try:
            data = json.loads(match.group(1))
        except json.JSONDecodeError:
            continue
        found = _find_marketplace_search(data)
        if found is None:
            continue
        items = parse_graphql_response({"data": {"marketplace_search": found}})
        if items:
            return items
    return []


def _find_marketplace_search(obj: Any) -> dict | None:
    if isinstance(obj, dict):
        candidate = obj.get("marketplace_search")
        if isinstance(candidate, dict):
            return candidate
        for value in obj.values():
            found = _find_marketplace_search(value)
            if found is not None:
                return found
    elif isinstance(obj, list):
        for value in obj:
            found = _find_marketplace_search(value)
            if found is not None:
                return found
    return None
```

- [ ] **Step 5: Rodar e confirmar que passa**

Run: `cd crawler && .venv/bin/pytest tests/facebook/test_fallback.py -v`
Expected: 3 testes PASS.

- [ ] **Step 6: Commit**

```bash
git add crawler/src/crawler/sources/facebook/fallback.py crawler/testdata/embedded_page.html \
  crawler/tests/facebook/test_fallback.py
git commit -m "feat(crawler): fall back to embedded json when no graphql response was captured"
```

---

### Task 8: Checkpoint / login-wall / rate-limit detection

Maps what the browser is showing to the `ErrorKind` values Task 2 defined, matching design spec §4.3's detection rules.

**Files:**
- Create: `crawler/src/crawler/sources/facebook/detect.py`
- Test: `crawler/tests/facebook/test_detect.py`

- [ ] **Step 1: Escrever os testes que falham**

`crawler/tests/facebook/test_detect.py`:

```python
from crawler.errors import ErrorKind
from crawler.sources.facebook.detect import classify_status, classify_url


def test_classify_url_detects_checkpoint():
    assert classify_url("https://www.facebook.com/checkpoint/1501092823525282/") is ErrorKind.CHECKPOINT


def test_classify_url_detects_two_step_verification():
    assert classify_url("https://www.facebook.com/two_step_verification/authentication/") is ErrorKind.CHECKPOINT


def test_classify_url_detects_login_wall():
    assert classify_url("https://www.facebook.com/login/?next=%2Fmarketplace%2F") is ErrorKind.LOGIN_REQUIRED


def test_classify_url_returns_none_for_a_normal_search_url():
    assert classify_url("https://www.facebook.com/marketplace/curitiba/search?query=civic") is None


def test_classify_status_detects_rate_limiting():
    assert classify_status(429) is ErrorKind.RATE_LIMITED


def test_classify_status_treats_server_errors_as_network():
    assert classify_status(500) is ErrorKind.NETWORK
    assert classify_status(503) is ErrorKind.NETWORK


def test_classify_status_returns_none_for_success():
    assert classify_status(200) is None
    assert classify_status(304) is None
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd crawler && .venv/bin/pytest tests/facebook/test_detect.py -v`
Expected: `ModuleNotFoundError: No module named 'crawler.sources.facebook.detect'`

- [ ] **Step 3: Implementar**

`crawler/src/crawler/sources/facebook/detect.py`:

```python
"""Classifies what the browser is showing, or what an HTTP response status
was, into the ErrorKind the Go backend's reaction table understands.
"""

from urllib.parse import urlsplit

from crawler.errors import ErrorKind

_CHECKPOINT_MARKERS = ("/checkpoint", "/two_step_verification", "/recover")
_LOGIN_MARKERS = ("/login",)


def classify_url(url: str) -> ErrorKind | None:
    path = urlsplit(url).path
    if any(marker in path for marker in _CHECKPOINT_MARKERS):
        return ErrorKind.CHECKPOINT
    if any(marker in path for marker in _LOGIN_MARKERS):
        return ErrorKind.LOGIN_REQUIRED
    return None


def classify_status(status: int) -> ErrorKind | None:
    if status == 429:
        return ErrorKind.RATE_LIMITED
    if status >= 500:
        return ErrorKind.NETWORK
    return None
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd crawler && .venv/bin/pytest tests/facebook/test_detect.py -v`
Expected: 7 testes PASS.

- [ ] **Step 5: Commit**

```bash
git add crawler/src/crawler/sources/facebook/detect.py crawler/tests/facebook/test_detect.py
git commit -m "feat(crawler): detect checkpoint, login wall and rate limiting"
```

---

### Task 9: Config

Mirrors `backend/internal/config`'s injected-`getenv` pattern (plan 1, Task 12) so this is testable without touching real environment variables.

**Files:**
- Create: `crawler/src/crawler/config.py`
- Test: `crawler/tests/test_config.py`

- [ ] **Step 1: Escrever os testes que falham**

`crawler/tests/test_config.py`:

```python
import pytest

from crawler.config import ConfigError, Settings


def env(values: dict) -> callable:
    return values.get


def test_defaults():
    s = Settings.load(env({"API_BASE_URL": "http://api:8080", "WORKER_TOKEN": "t"}))
    assert s.api_base_url == "http://api:8080"
    assert s.worker_token == "t"
    assert s.sources == ["facebook_marketplace"]
    assert s.types == ["search"]
    assert s.poll_interval_seconds == 10
    assert s.crawler_version == "0.1.0"
    assert s.profile_dir == "/data/profile"
    assert s.worker_id  # non-empty, derived from hostname when not set


def test_overrides():
    s = Settings.load(env({
        "API_BASE_URL": "http://api:8080", "WORKER_TOKEN": "t",
        "WORKER_ID": "custom-1", "SOURCES": "facebook_marketplace,olx",
        "TYPES": "search,enrich", "POLL_INTERVAL_SECONDS": "30",
        "PROFILE_DIR": "/custom/profile",
    }))
    assert s.worker_id == "custom-1"
    assert s.sources == ["facebook_marketplace", "olx"]
    assert s.types == ["search", "enrich"]
    assert s.poll_interval_seconds == 30
    assert s.profile_dir == "/custom/profile"


def test_missing_required_vars_raise():
    with pytest.raises(ConfigError):
        Settings.load(env({"WORKER_TOKEN": "t"}))
    with pytest.raises(ConfigError):
        Settings.load(env({"API_BASE_URL": "http://api:8080"}))


def test_bad_poll_interval_raises():
    with pytest.raises(ConfigError):
        Settings.load(env({"API_BASE_URL": "http://api:8080", "WORKER_TOKEN": "t",
                            "POLL_INTERVAL_SECONDS": "soon"}))
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd crawler && .venv/bin/pytest tests/test_config.py -v`
Expected: `ModuleNotFoundError: No module named 'crawler.config'`

- [ ] **Step 3: Implementar**

`crawler/src/crawler/config.py`:

```python
"""Reads worker settings from environment variables.

Mirrors backend/internal/config's injected-getenv pattern (plan 1, Task 12)
for the same reason: testable without os.environ side effects.
"""

import socket
from dataclasses import dataclass, field
from typing import Callable


class ConfigError(Exception):
    pass


def _or(value: str | None, fallback: str) -> str:
    return value if value else fallback


def _split_csv(value: str) -> list[str]:
    return [part.strip() for part in value.split(",") if part.strip()]


@dataclass
class Settings:
    api_base_url: str
    worker_token: str
    worker_id: str
    sources: list[str] = field(default_factory=lambda: ["facebook_marketplace"])
    types: list[str] = field(default_factory=lambda: ["search"])
    poll_interval_seconds: int = 10
    crawler_version: str = "0.1.0"
    profile_dir: str = "/data/profile"

    @classmethod
    def load(cls, getenv: Callable[[str], str | None]) -> "Settings":
        api_base_url = getenv("API_BASE_URL")
        if not api_base_url:
            raise ConfigError("API_BASE_URL is required")
        worker_token = getenv("WORKER_TOKEN")
        if not worker_token:
            raise ConfigError("WORKER_TOKEN is required")

        try:
            poll_interval = int(_or(getenv("POLL_INTERVAL_SECONDS"), "10"))
        except ValueError as e:
            raise ConfigError(f"POLL_INTERVAL_SECONDS: {e}") from e

        worker_id = _or(getenv("WORKER_ID"), socket.gethostname())
        return cls(
            api_base_url=api_base_url,
            worker_token=worker_token,
            worker_id=worker_id,
            sources=_split_csv(_or(getenv("SOURCES"), "facebook_marketplace")),
            types=_split_csv(_or(getenv("TYPES"), "search")),
            poll_interval_seconds=poll_interval,
            crawler_version=_or(getenv("CRAWLER_VERSION"), "0.1.0"),
            profile_dir=_or(getenv("PROFILE_DIR"), "/data/profile"),
        )
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd crawler && .venv/bin/pytest tests/test_config.py -v`
Expected: 4 testes PASS.

- [ ] **Step 5: Commit**

```bash
git add crawler/src/crawler/config.py crawler/tests/test_config.py
git commit -m "feat(crawler): load worker settings from environment"
```

---

### Task 10: Worker loop

The control flow (`claim → adapter.run → submit result/fail`) is tested with a fake API client and a fake adapter — no HTTP, no Playwright, just checking that the right calls happen in the right order with the right arguments.

**Files:**
- Create: `crawler/src/crawler/worker.py`
- Test: `crawler/tests/test_worker.py`

- [ ] **Step 1: Escrever os testes que falham**

`crawler/tests/test_worker.py`:

```python
import pytest

from crawler.contracts import ClaimedJob, PageLimits, RegionPayload, ResultItem, SearchPayload, Stats, Outcome
from crawler.errors import CrawlError, ErrorKind
from crawler.worker import run_once


def make_job(job_id="j1", attempt=1, source="facebook_marketplace"):
    return ClaimedJob(
        job_id=job_id, attempt=attempt, type="search", source=source, stop_before=None,
        search=SearchPayload(id="s1", keyword="Honda Civic EG", filters={}),
        region=RegionPayload(id="curitiba-pr", fb_location="curitiba"),
        limits=PageLimits(max_pages=3, max_items=120),
    )


class FakeAPI:
    def __init__(self, claim_result=None):
        self._claim_result = claim_result
        self.registered = False
        self.results: list[tuple] = []
        self.fails: list[tuple] = []

    def claim(self, sources, types):
        self.claim_sources, self.claim_types = sources, types
        return self._claim_result

    def submit_result(self, job_id, attempt, crawler_version, parser_version, items, stats):
        self.results.append((job_id, attempt, crawler_version, parser_version, items, stats))
        return Stats(new=len(items), changed=0, seen=0, invalid=0, alerts=0)

    def submit_fail(self, job_id, attempt, error_kind, message):
        self.fails.append((job_id, attempt, error_kind, message))
        return Outcome(status="queued", run_after=None)


class FakeAdapter:
    source = "facebook_marketplace"

    def __init__(self, items=None, error=None):
        self._items = items or []
        self._error = error
        self.ran_with = None

    def run(self, job):
        self.ran_with = job
        if self._error is not None:
            raise self._error
        return self._items


def test_run_once_returns_false_when_nothing_to_claim():
    api = FakeAPI(claim_result=None)
    claimed = run_once(api, {"facebook_marketplace": FakeAdapter()}, sources=["facebook_marketplace"],
                        types=["search"], crawler_version="0.1.0", parser_version="fb-1")
    assert claimed is False
    assert api.claim_sources == ["facebook_marketplace"]
    assert api.claim_types == ["search"]


def test_run_once_submits_result_on_success():
    job = make_job()
    adapter = FakeAdapter(items=[ResultItem(external_id="1", raw={"id": "1"})])
    api = FakeAPI(claim_result=job)
    claimed = run_once(api, {"facebook_marketplace": adapter}, sources=["facebook_marketplace"],
                        types=["search"], crawler_version="0.1.0", parser_version="fb-1")
    assert claimed is True
    assert adapter.ran_with is job
    assert len(api.results) == 1
    result_job_id, attempt, crawler_version, parser_version, items, stats = api.results[0]
    assert result_job_id == "j1" and attempt == 1 and crawler_version == "0.1.0"
    assert items[0].external_id == "1"
    assert "duration_ms" in stats
    assert api.fails == []


def test_run_once_submits_fail_on_crawl_error():
    job = make_job()
    adapter = FakeAdapter(error=CrawlError(ErrorKind.CHECKPOINT, "hit a checkpoint"))
    api = FakeAPI(claim_result=job)
    run_once(api, {"facebook_marketplace": adapter}, sources=["facebook_marketplace"],
              types=["search"], crawler_version="0.1.0", parser_version="fb-1")
    assert api.results == []
    assert len(api.fails) == 1
    fail_job_id, attempt, error_kind, message = api.fails[0]
    assert fail_job_id == "j1" and attempt == 1
    assert error_kind == "checkpoint"
    assert message == "hit a checkpoint"


def test_run_once_treats_unclassified_exception_as_network():
    job = make_job()
    adapter = FakeAdapter(error=RuntimeError("playwright blew up"))
    api = FakeAPI(claim_result=job)
    run_once(api, {"facebook_marketplace": adapter}, sources=["facebook_marketplace"],
              types=["search"], crawler_version="0.1.0", parser_version="fb-1")
    assert len(api.fails) == 1
    _, _, error_kind, message = api.fails[0]
    assert error_kind == "network"
    assert "playwright blew up" in message


def test_run_once_raises_on_unknown_source():
    job = make_job(source="olx")
    api = FakeAPI(claim_result=job)
    with pytest.raises(KeyError):
        run_once(api, {"facebook_marketplace": FakeAdapter()}, sources=["facebook_marketplace"],
                  types=["search"], crawler_version="0.1.0", parser_version="fb-1")
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd crawler && .venv/bin/pytest tests/test_worker.py -v`
Expected: `ModuleNotFoundError: No module named 'crawler.worker'`

- [ ] **Step 3: Implementar**

`crawler/src/crawler/worker.py`:

```python
"""The claim -> run adapter -> submit result/fail loop.

Deliberately has no business logic: it doesn't decide what counts as a
duplicate, doesn't compute alerts, doesn't touch a database. It only moves
one job through one adapter and reports what happened, exactly as the
design spec's §4 split between Go (business logic) and Python (browser
work) requires.
"""

import time
from typing import Protocol

from crawler.contracts import ResultItem, Stats, Outcome
from crawler.errors import CrawlError, ErrorKind
from crawler.sources.base import Adapter


class APIClientProtocol(Protocol):
    def claim(self, sources: list[str], types: list[str]): ...
    def submit_result(self, job_id: str, attempt: int, crawler_version: str,
                       parser_version: str, items: list[ResultItem], stats: dict) -> Stats: ...
    def submit_fail(self, job_id: str, attempt: int, error_kind: str, message: str) -> Outcome: ...


def run_once(
    api: APIClientProtocol,
    adapters: dict[str, Adapter],
    sources: list[str],
    types: list[str],
    crawler_version: str,
    parser_version: str,
) -> bool:
    """Claims and processes at most one job. Returns False if there was
    nothing to claim, True otherwise (regardless of whether it succeeded
    or failed — "a job was processed" is the signal the caller's poll loop
    needs, to decide whether to claim again immediately or wait).
    """
    job = api.claim(sources=sources, types=types)
    if job is None:
        return False

    adapter = adapters[job.source]  # KeyError here means misconfiguration: a source
    # the scheduler assigns jobs for but this worker has no adapter for. That should
    # never happen if SOURCES/TYPES are configured correctly, so it's not caught here.

    started = time.monotonic()
    try:
        items = adapter.run(job)
    except CrawlError as e:
        api.submit_fail(job_id=job.job_id, attempt=job.attempt, error_kind=e.kind.value, message=str(e))
        return True
    except Exception as e:  # noqa: BLE001 - deliberately broad: anything unclassified is "network"
        api.submit_fail(job_id=job.job_id, attempt=job.attempt,
                         error_kind=ErrorKind.NETWORK.value, message=str(e))
        return True

    duration_ms = int((time.monotonic() - started) * 1000)
    api.submit_result(
        job_id=job.job_id, attempt=job.attempt, crawler_version=crawler_version,
        parser_version=parser_version, items=items,
        stats={"pages": 1, "duration_ms": duration_ms},
    )
    return True


def run_forever(
    api: APIClientProtocol,
    adapters: dict[str, Adapter],
    sources: list[str],
    types: list[str],
    crawler_version: str,
    parser_version: str,
    poll_interval_seconds: int,
    stop_event=None,
) -> None:
    """Loops run_once forever (or until stop_event is set). Sleeps between
    claims only when there was nothing to do, so a busy queue gets drained
    without an artificial delay between jobs.
    """
    while stop_event is None or not stop_event.is_set():
        claimed = run_once(api, adapters, sources, types, crawler_version, parser_version)
        if not claimed:
            time.sleep(poll_interval_seconds)
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd crawler && .venv/bin/pytest tests/test_worker.py -v`
Expected: 5 testes PASS.

- [ ] **Step 5: Commit**

```bash
git add crawler/src/crawler/worker.py crawler/tests/test_worker.py
git commit -m "feat(crawler): add the claim/run/submit worker loop"
```

---

### Task 11: Playwright session + Facebook adapter

This is the one module that actually drives a browser. It is **not unit tested** beyond the pure `is_logged_in` helper — there's no way to exercise a real Playwright browser against real Facebook in an automated test without violating the constraint in "Why no Playwright on the host" above. Its correctness is checked structurally now (imports resolve, types line up) and functionally in Task 13 (manual, against the user's real session).

**Files:**
- Create: `crawler/src/crawler/sources/facebook/session.py`
- Create: `crawler/src/crawler/sources/facebook/run.py`
- Create: `crawler/scripts/login.py`
- Test: `crawler/tests/facebook/test_session.py`

- [ ] **Step 1: Escrever o teste que falha (só a parte pura)**

`crawler/tests/facebook/test_session.py`:

```python
from crawler.sources.facebook.session import is_logged_in


def test_detects_a_logged_in_session():
    assert is_logged_in([{"name": "c_user", "value": "123456"}, {"name": "xs", "value": "abc"}])


def test_detects_a_logged_out_session():
    assert not is_logged_in([{"name": "xs", "value": "abc"}])
    assert not is_logged_in([{"name": "c_user", "value": ""}])
    assert not is_logged_in([])
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd crawler && .venv/bin/pytest tests/facebook/test_session.py -v`
Expected: `ModuleNotFoundError: No module named 'crawler.sources.facebook.session'`

- [ ] **Step 3: Implementar a parte pura**

`crawler/src/crawler/sources/facebook/session.py`:

```python
"""Persistent-context session helpers."""

LOGIN_COOKIE_NAME = "c_user"


def is_logged_in(cookies: list[dict]) -> bool:
    """`cookies` is Playwright's BrowserContext.cookies() result: a list of
    dicts with at least "name" and "value". Returns True if a non-empty
    Facebook session cookie is present.
    """
    return any(c.get("name") == LOGIN_COOKIE_NAME and c.get("value") for c in cookies)
```

- [ ] **Step 4: Rodar e confirmar que passa**

Run: `cd crawler && .venv/bin/pytest tests/facebook/test_session.py -v`
Expected: 2 testes PASS.

- [ ] **Step 5: Implementar o adapter (sem teste automatizado — veja a nota acima)**

`crawler/src/crawler/sources/facebook/run.py`:

```python
"""Drives Playwright end to end for one search job. Not unit tested — see
the plan's Task 11 note. Verified manually in Task 13, and structurally by
`python3 -c "import crawler.sources.facebook.run"` succeeding (Step 6).
"""

import asyncio

from playwright.async_api import Response, async_playwright

from crawler.contracts import ClaimedJob, ResultItem
from crawler.errors import CrawlError, ErrorKind
from crawler.sources.facebook.capture import parse_graphql_response
from crawler.sources.facebook.constants import SOURCE
from crawler.sources.facebook.detect import classify_status, classify_url
from crawler.sources.facebook.fallback import parse_embedded_json
from crawler.sources.facebook.session import is_logged_in
from crawler.sources.facebook.url import build_search_url


class FacebookAdapter:
    source = SOURCE

    def __init__(self, profile_dir: str, scroll_pause_seconds: float = 3.0):
        self.profile_dir = profile_dir
        self.scroll_pause_seconds = scroll_pause_seconds

    def run(self, job: ClaimedJob) -> list[ResultItem]:
        return asyncio.run(self._run_async(job))

    async def _run_async(self, job: ClaimedJob) -> list[ResultItem]:
        async with async_playwright() as pw:
            context = await pw.chromium.launch_persistent_context(
                self.profile_dir, headless=True, viewport={"width": 1280, "height": 900},
            )
            try:
                return await self._search(context, job)
            finally:
                await context.close()

    async def _search(self, context, job: ClaimedJob) -> list[ResultItem]:
        if not is_logged_in(await context.cookies()):
            raise CrawlError(ErrorKind.LOGIN_REQUIRED, "no active facebook session in the browser profile")

        captured: list[dict] = []
        page = await context.new_page()

        async def on_response(response: Response) -> None:
            status_kind = classify_status(response.status)
            if status_kind is not None:
                raise CrawlError(status_kind, f"http {response.status} on {response.url}")
            if "/api/graphql/" not in response.url:
                return
            try:
                body = await response.json()
            except Exception:  # noqa: BLE001 - not every graphql call returns JSON we care about
                return
            items = parse_graphql_response(body)
            if items:
                captured.extend(items)

        page.on("response", lambda r: asyncio.ensure_future(on_response(r)))

        url = build_search_url(job.region.fb_location, job.search.keyword, job.search.filters)
        await page.goto(url, wait_until="domcontentloaded")

        checkpoint = classify_url(page.url)
        if checkpoint is not None:
            raise CrawlError(checkpoint, f"redirected to {page.url}")

        if not captured:
            captured.extend(parse_embedded_json(await page.content()))

        pages_scrolled = 0
        while pages_scrolled < job.limits.max_pages and len(captured) < job.limits.max_items:
            await page.mouse.wheel(0, 2000)
            await page.wait_for_timeout(int(self.scroll_pause_seconds * 1000))
            pages_scrolled += 1
            checkpoint = classify_url(page.url)
            if checkpoint is not None:
                raise CrawlError(checkpoint, f"redirected to {page.url} mid-scroll")

        if not captured:
            raise CrawlError(ErrorKind.LAYOUT_CHANGED, "no listings captured from graphql or embedded json")

        seen_external_ids: set[str] = set()
        items: list[ResultItem] = []
        for raw in captured:
            if raw["external_id"] in seen_external_ids:
                continue
            seen_external_ids.add(raw["external_id"])
            items.append(ResultItem(external_id=raw["external_id"], raw=raw["raw"]))
            if len(items) >= job.limits.max_items:
                break
        return items
```

- [ ] **Step 6: Confirmar que o módulo importa (checagem estrutural, sem browser)**

Run: `cd crawler && .venv/bin/pip install -q playwright && .venv/bin/python -c "import crawler.sources.facebook.run"`
Expected: sem saída (import bem-sucedido — confirma que os tipos/assinaturas usados de `playwright.async_api` existem na versão instalada, sem precisar abrir um browser).

- [ ] **Step 7: Escrever o script de login manual**

`crawler/scripts/login.py`:

```python
"""Manual login entrypoint. Run this once (and again whenever the session
expires) inside the crawler container with WORKER_MODE=login — see the
Dockerfile and compose.yaml. It opens a real, visible Chromium window
(visible via noVNC, not headless) pointed at Facebook's login page, and
waits for you to log in by hand. Never attempts to fill in credentials,
solve a CAPTCHA, or complete 2FA automatically — see design spec §19/§34.
"""

import asyncio
import os
import sys

from playwright.async_api import async_playwright

from crawler.sources.facebook.session import is_logged_in

PROFILE_DIR = os.environ.get("PROFILE_DIR", "/data/profile")
POLL_SECONDS = 2
TIMEOUT_SECONDS = 15 * 60


async def main() -> None:
    print(f"Opening a browser against profile {PROFILE_DIR!r}. Log in by hand; "
          f"this script exits automatically once it detects a session cookie, "
          f"or after {TIMEOUT_SECONDS // 60} minutes.")
    async with async_playwright() as pw:
        context = await pw.chromium.launch_persistent_context(
            PROFILE_DIR, headless=False, viewport={"width": 1280, "height": 900},
        )
        try:
            page = await context.new_page()
            await page.goto("https://www.facebook.com/login")
            waited = 0
            while waited < TIMEOUT_SECONDS:
                if is_logged_in(await context.cookies()):
                    print("Logged in. Session saved to the profile directory.")
                    return
                await asyncio.sleep(POLL_SECONDS)
                waited += POLL_SECONDS
            print("Timed out waiting for login.", file=sys.stderr)
            sys.exit(1)
        finally:
            await context.close()


if __name__ == "__main__":
    asyncio.run(main())
```

- [ ] **Step 8: Rodar a suíte inteira e confirmar que nada quebrou**

Run: `cd crawler && .venv/bin/pytest -v`
Expected: todos os testes anteriores continuam PASS (o adapter e o script de login não têm teste automatizado próprio, mas não devem quebrar a importação de nada).

- [ ] **Step 9: Commit**

```bash
git add crawler/src/crawler/sources/facebook/session.py crawler/src/crawler/sources/facebook/run.py \
  crawler/scripts/login.py crawler/tests/facebook/test_session.py
git commit -m "feat(crawler): add playwright session handling, facebook adapter and login script"
```

---

### Task 12: Worker entrypoint, Docker image, compose wiring, and a real (Facebook-free) end-to-end check

The automated check in this task brings up the **real** Postgres + Go API + Python crawler together over Docker and proves the whole pipeline reacts correctly to "not logged in yet" — without the crawler ever making a single request to facebook.com. `FacebookAdapter.run()` checks `is_logged_in()` on a fresh, empty browser profile *before* calling `page.goto(...)`, so this exercises real Playwright (a real Chromium launches), the real HTTP contract, the real scheduler, and the real backend's checkpoint/login_required reaction (source pauses, system alert fires) — with zero network contact to Facebook.

**Files:**
- Create: `crawler/scripts/run_worker.py`
- Create: `crawler/docker/entrypoint.sh`
- Create: `crawler/Dockerfile`
- Modify: `compose.yaml` (add the `crawler` service and its two named volumes)

- [ ] **Step 1: Escrever o entrypoint do worker**

`crawler/scripts/run_worker.py`:

```python
"""Command: registers this worker and runs the claim/run/submit loop forever."""

import os
import socket

from crawler.api import APIClient
from crawler.config import Settings
from crawler.sources.facebook.run import FacebookAdapter
from crawler.worker import run_forever


def main() -> None:
    settings = Settings.load(os.environ.get)
    api = APIClient(base_url=settings.api_base_url, token=settings.worker_token,
                     worker_id=settings.worker_id)
    api.register(hostname=socket.gethostname(), crawler_version=settings.crawler_version,
                  sources=settings.sources)
    adapters = {"facebook_marketplace": FacebookAdapter(profile_dir=settings.profile_dir)}
    print(f"worker {settings.worker_id!r} registered, polling for {settings.sources} jobs...")
    run_forever(
        api, adapters, sources=settings.sources, types=settings.types,
        crawler_version=settings.crawler_version, parser_version="fb-1",
        poll_interval_seconds=settings.poll_interval_seconds,
    )


if __name__ == "__main__":
    main()
```

(No periodic heartbeat loop here — this plan is intentionally scoped to the crawl pipeline itself. The dashboard that would display worker heartbeats is plan 4, out of scope here; `register()` alone is enough for plan 2's goal of proving the end-to-end flow works. Revisit if plan 4 needs liveness data this doesn't provide.)

- [ ] **Step 2: Escrever o entrypoint do container**

`crawler/docker/entrypoint.sh`:

```bash
#!/bin/sh
set -e

# Xvfb gives Chromium a virtual display; x11vnc exposes it over VNC;
# websockify bridges that to the browser-based noVNC client on :6080.
# Needed even in "worker" mode because Playwright still launches a real
# (non-headless-in-the-Chromium-sense, but display-less-without-Xvfb) browser.
Xvfb :99 -screen 0 1280x900x24 &
x11vnc -display :99 -nopw -listen 0.0.0.0 -xkb -forever -quiet &
websockify --web /usr/share/novnc 6080 localhost:5900 &
sleep 1

if [ "$WORKER_MODE" = "login" ]; then
    exec python scripts/login.py
else
    exec python scripts/run_worker.py
fi
```

- [ ] **Step 3: Escrever o Dockerfile**

`crawler/Dockerfile`:

```dockerfile
FROM python:3.13-slim

ENV PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    DISPLAY=:99

# novnc's package layout (e.g. where its static web assets land, e.g.
# /usr/share/novnc) can vary across Debian releases — if the entrypoint's
# websockify --web path doesn't exist on the base image in use, check
# `dpkg -L novnc` inside the built image and adjust docker/entrypoint.sh.
RUN apt-get update && apt-get install -y --no-install-recommends \
      xvfb x11vnc novnc websockify \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY pyproject.toml ./
COPY src ./src
COPY scripts ./scripts
RUN pip install -e . \
    && pip install playwright \
    && playwright install --with-deps chromium

COPY docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 6080
VOLUME ["/data/profile", "/data/debug"]
ENTRYPOINT ["/entrypoint.sh"]
```

- [ ] **Step 4: Adicionar o serviço `crawler` ao `compose.yaml`**

Read the current `compose.yaml` (has `postgres` and `api` from plan 1). Insert `crawler` below `api`, above `volumes:`, and add its two named volumes to the top-level `volumes:` block:

```yaml
  crawler:
    build: ./crawler
    environment:
      API_BASE_URL: http://api:8080
      WORKER_TOKEN: ${WORKER_TOKEN:?set WORKER_TOKEN in .env}
      WORKER_MODE: ${WORKER_MODE:-worker}
    volumes:
      - crawler_profile:/data/profile
      - crawler_debug:/data/debug
    ports:
      - "127.0.0.1:6080:6080"
    shm_size: "1gb"
    depends_on:
      - api
    restart: unless-stopped
```

Top-level `volumes:` block becomes:

```yaml
volumes:
  pgdata:
  crawler_profile:
  crawler_debug:
```

- [ ] **Step 5: Build e checagem estrutural (sem rede nenhuma)**

Run:
```bash
cd /path/to/scrapperbook
docker compose build crawler
docker compose run --rm --entrypoint python crawler \
  -c "import crawler.worker, crawler.sources.facebook.run, crawler.scripts" 2>&1 | tail -5
```
(The last import will fail with `ModuleNotFoundError: No module named 'crawler.scripts'` — that's expected and fine, `scripts/` isn't a package, it's run as a script; drop that half of the import and just confirm the first two succeed.)

Run: `docker compose run --rm --entrypoint python crawler -c "import crawler.worker, crawler.sources.facebook.run; print('OK')"`
Expected: `OK` (confirms Playwright installed correctly inside the image and every module's imports resolve).

- [ ] **Step 6: Rodar o teste real fim-a-fim, sem nenhum contato com o Facebook**

Reuse plan 1's dev seed (`backend/seed/dev.sql`) so there's a real due search for the scheduler to turn into a job.

```bash
cd /path/to/scrapperbook
QUIET_HOURS=off docker compose up -d --build postgres api crawler
docker compose exec -T postgres psql -U monitor -d monitor < backend/seed/dev.sql
docker compose logs -f crawler &
LOGS_PID=$!
sleep 90
kill $LOGS_PID
```
Expected in the crawler logs: a line like `worker '<hostname>' registered, polling for ['facebook_marketplace'] jobs...`, then (once the scheduler has ticked and the crawler has claimed) no crash, no traceback — the adapter raises `CrawlError(LOGIN_REQUIRED, ...)` internally, which `run_once` catches and reports via `submit_fail`, so nothing should print an unhandled exception.

Then check the database state:
```bash
docker compose exec -T postgres psql -U monitor -d monitor -c \
  "SELECT status, error_kind FROM jobs ORDER BY created_at DESC LIMIT 1;"
docker compose exec -T postgres psql -U monitor -d monitor -c \
  "SELECT status, note FROM source_sessions WHERE source = 'facebook_marketplace';"
docker compose exec -T postgres psql -U monitor -d monitor -c \
  "SELECT kind, message FROM notifications WHERE kind = 'system' ORDER BY created_at DESC LIMIT 1;"
```
Expected:
- the latest job: `status = queued`, `error_kind = login_required` (requeued without consuming an attempt, per plan 1's reaction table — it'll keep showing up here every time the scheduler/crawler poll, which is correct: the source is paused, not the individual job).
- `source_sessions`: `status = needs_attention`, `note` mentioning the session needing attention.
- a `system` notification exists mentioning `login_required` or "sessão precisa de atenção".

This is the real backend code (plan 1, unmodified) reacting to a real Python worker's real (if deliberately unauthenticated) Playwright run — the strongest check available without an actual Facebook login.

- [ ] **Step 7: Derrubar**

Run: `docker compose down -v`
Expected: containers and the fresh volumes (including the crawler's never-logged-in profile) removed, so Task 13 starts from a clean slate.

- [ ] **Step 8: Commit**

```bash
git add crawler/scripts/run_worker.py crawler/docker/entrypoint.sh crawler/Dockerfile compose.yaml
git commit -m "feat(crawler): add docker image, compose wiring and worker entrypoint"
```

---

### Task 13: Manual login, real fixture capture, and the real smoke test — human only, not a subagent task

**Do not dispatch this task to a subagent, automated or otherwise.** Every step here requires the actual human user's own Facebook account, their own judgment about what's safe to capture and share, and their own decision about when to stop. This is the one place in the whole plan where that line matters — see design spec §19/§34: no automated login, no CAPTCHA/2FA handling, no credential storage in code.

**Files:**
- Modify (by hand, after capturing real data): `backend/internal/source/facebook/testdata/listing_node.json` (plan 1, Task 4's synthetic fixture)
- Modify (by hand): `crawler/testdata/graphql_search_response.json` and `crawler/testdata/embedded_page.html` (this plan's Task 6/7 synthetic fixtures)
- Modify (by hand): `backend/seed/dev.sql` (plan 1, Task 13's `fb_location = 'curitiba'` placeholder)

- [ ] **Step 1: Subir a stack e abrir o noVNC**

```bash
cd /path/to/scrapperbook
docker compose up -d --build postgres api
WORKER_MODE=login docker compose up -d --build crawler
```
Open `http://localhost:6080/vnc.html` in a regular browser and connect (no password set — it's bound to `127.0.0.1` only). You should see a Chromium window open to Facebook's login page.

- [ ] **Step 2: Logar manualmente**

Log in with your own Facebook account inside that noVNC window, exactly as you would on your phone or laptop — including any 2FA or security check Facebook shows you. Nothing in this repo fills in anything for you here. Once `scripts/login.py` detects the session cookie it prints "Logged in." and exits; the container then stops (or restart it in normal worker mode below).

- [ ] **Step 3: Confirmar o `fb_location` real de Curitiba**

Still inside that same noVNC browser session (now logged in), navigate to `https://www.facebook.com/marketplace/` and use Facebook's own location picker to select Curitiba, PR. Read the resulting URL's location segment directly from the address bar (it'll be either a slug like `curitiba` or a numeric place ID — research done during this plan's brainstorming found Facebook sometimes uses numeric IDs for Brazilian regions rather than slugs, so don't assume it's `curitiba` until you've actually seen it).

Update `backend/seed/dev.sql`'s `fb_location` value (currently the placeholder `'curitiba'`) to whatever you actually saw, and update the comment above it that says the value is provisional.

- [ ] **Step 4: Capturar uma resposta real (sanitizada)**

In that same browser, open DevTools → Network, filter for `graphql`, and run one real search (any keyword is fine — doesn't have to be "Honda Civic EG"). Find one `/api/graphql/` response whose body matches the `data.marketplace_search.feed_units.edges[].node.listing` shape this plan's `capture.py` (Task 6) parses.

Copy ONE listing node from that response. Before saving it anywhere in this repo:
- Replace the real seller name and seller id with placeholders (`"Vendedor Teste"`, `"555"`) — do not commit a real person's name.
- Keep the real field *names* and *shape* (that's the entire point of replacing the synthetic fixture).
- Replace the real listing title/price with something unremarkable if the original listing is still identifiable, to avoid pointing at a specific real person's real-world item.

Use this sanitized node to replace:
- `backend/internal/source/facebook/testdata/listing_node.json` (plan 1, Task 4) — keep it a single node, same test assertions in `normalizer_test.go` should still pass if the sanitized shape matches (adjust the test's expected values if field names turned out to differ from the synthetic version).
- `crawler/testdata/graphql_search_response.json` (this plan's Task 6) — wrap it in the same `edges`/envelope structure the existing fixture uses.
- `crawler/testdata/embedded_page.html` (this plan's Task 7) — re-embed it in the same `<script type="application/json">` wrapper structure.

- [ ] **Step 5: Rodar as suítes automatizadas de novo com os fixtures reais**

```bash
cd backend && go test ./internal/source/facebook/ -v
cd ../crawler && .venv/bin/pytest tests/facebook/ -v
```
Expected: all tests that read these fixtures still PASS. If a real field turned out to be named or shaped differently than the synthetic guess, fix the normalizer/parser code (not the fixture) to match reality — the fixture is now ground truth.

- [ ] **Step 6: O smoke test real, com login de verdade**

```bash
docker compose up -d crawler   # back to normal worker mode (WORKER_MODE unset/"worker")
docker compose logs -f crawler
```
Within a few minutes (scheduler tick + crawler poll), you should see your worker claim the seeded "Honda Civic EG" / Curitiba search, actually navigate Facebook Marketplace, and submit a real result. Confirm:
```bash
docker compose exec -T postgres psql -U monitor -d monitor -c \
  "SELECT external_id, title, price_cents, status FROM listings ORDER BY first_seen_at DESC LIMIT 5;"
```
shows real listings, not the synthetic Honda Civic EG from plan 1's smoke test.

- [ ] **Step 7: Derrubar**

Run: `docker compose down`
(Not `-v` this time — you want to keep the logged-in profile volume around for next time, instead of having to log in again.)

- [ ] **Step 8: Commit**

```bash
git add backend/internal/source/facebook/testdata/listing_node.json \
  crawler/testdata/graphql_search_response.json crawler/testdata/embedded_page.html \
  backend/seed/dev.sql
git commit -m "fix: replace synthetic facebook fixtures with a sanitized real capture"
```

---

### Task 14: Verificação final

- [ ] **Step 1: Suíte Python completa**

Run: `cd crawler && .venv/bin/pytest -v`
Expected: todos os pacotes de teste `PASSED` (errors, contracts, config, api, worker, facebook/url, facebook/capture, facebook/fallback, facebook/detect, facebook/session).

- [ ] **Step 2: Suíte Go completa (sem regressão do plano 1)**

Run: `cd backend && go vet ./... && go test ./... -count=1`
Expected: todos os pacotes `ok` — confirma que nada neste plano quebrou o backend.

- [ ] **Step 3: Conferir critérios deste plano**

- [ ] Toda função pura (`url.py`, `capture.py`, `fallback.py`, `detect.py`, `session.is_logged_in`) tem teste de fixture, sem depender de browser.
- [ ] `worker.py` tem cobertura de: claim vazio, sucesso, `CrawlError` classificado, exceção não classificada tratada como `network`.
- [ ] `docs/contracts/job.schema.json` e `result.schema.json` existem e um teste garante que não saem de sincronia com `contracts.py`.
- [ ] A imagem Docker builda e importa corretamente dentro do container (Task 12, Step 5).
- [ ] O teste fim-a-fim sem Facebook (Task 12, Step 6) resultou em `source_sessions.status = needs_attention` e um alerta de sistema — prova que o pipeline reage certo a "não logado" sem nunca tocar o Facebook de verdade.
- [ ] Task 13 (login real, fixture real, smoke test real) executado pelo humano — **não** por este processo de implementação automatizado.

## Próximo plano

Plano 3 (notificação Discord + API pública) consome os dados que este crawler + o backend do plano 1 já produzem. Nenhuma dependência nova deste plano além do que os planos 1 e 2 já entregam.
