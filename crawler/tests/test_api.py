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
