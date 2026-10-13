"""The claim -> run adapter -> submit result/fail loop.

Deliberately has no business logic: it doesn't decide what counts as a
duplicate, doesn't compute alerts, doesn't touch a database. It only moves
one job through one adapter and reports what happened, exactly as the
design spec's split between Go (business logic) and Python (browser
work) requires.
"""

import logging
import threading
import time
from typing import Protocol

logger = logging.getLogger(__name__)

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
    stop_event: threading.Event | None = None,
) -> None:
    """Loops run_once forever (or until stop_event is set). Sleeps between
    claims only when there was nothing to do, so a busy queue gets drained
    without an artificial delay between jobs. The sleep is interruptible
    (stop_event.wait, not time.sleep), so shutdown isn't delayed by a full
    poll interval.

    A run_once failure (e.g. the API call that reports a crawl failure
    itself fails) is caught and logged here rather than left to crash the
    whole process — the same reasoning as the Go backend's per-tick
    recover() in main.go: one bad iteration shouldn't end the worker.
    """
    stop_event = stop_event or threading.Event()
    while not stop_event.is_set():
        try:
            claimed = run_once(api, adapters, sources, types, crawler_version, parser_version)
        except Exception:
            logger.exception("run_once failed; will retry next poll")
            claimed = False
        if not claimed:
            stop_event.wait(poll_interval_seconds)
