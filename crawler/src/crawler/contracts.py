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
