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
