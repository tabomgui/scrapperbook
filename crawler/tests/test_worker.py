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


import threading

from crawler.worker import run_forever


def test_run_forever_stops_immediately_if_stop_event_already_set():
    event = threading.Event()
    event.set()
    api = FakeAPI(claim_result=None)
    run_forever(api, {"facebook_marketplace": FakeAdapter()}, sources=["facebook_marketplace"],
                types=["search"], crawler_version="0.1.0", parser_version="fb-1",
                poll_interval_seconds=0, stop_event=event)
    # No assertion needed beyond "this returns at all" — a failure here means
    # an infinite loop, which would hang the test suite instead of failing cleanly.


def test_run_forever_stops_after_n_claims():
    job = make_job()
    adapter = FakeAdapter(items=[])
    event = threading.Event()
    call_count = {"n": 0}

    class CountingAPI(FakeAPI):
        def claim(self, sources, types):
            call_count["n"] += 1
            if call_count["n"] >= 3:
                event.set()
            return job

    api = CountingAPI(claim_result=job)
    run_forever(api, {"facebook_marketplace": adapter}, sources=["facebook_marketplace"],
                types=["search"], crawler_version="0.1.0", parser_version="fb-1",
                poll_interval_seconds=0, stop_event=event)
    assert call_count["n"] == 3
    assert len(api.results) == 3


def test_run_forever_survives_a_run_once_exception_and_keeps_polling():
    event = threading.Event()
    call_count = {"n": 0}

    class FlakyAPI(FakeAPI):
        def claim(self, sources, types):
            call_count["n"] += 1
            if call_count["n"] == 1:
                raise RuntimeError("transient network blip reporting a result")
            if call_count["n"] >= 2:
                event.set()
            return None

    api = FlakyAPI()
    # Should not raise despite the first claim() call blowing up.
    run_forever(api, {"facebook_marketplace": FakeAdapter()}, sources=["facebook_marketplace"],
                types=["search"], crawler_version="0.1.0", parser_version="fb-1",
                poll_interval_seconds=0, stop_event=event)
    assert call_count["n"] == 2
