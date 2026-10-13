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

    def __enter__(self) -> "APIClient":
        return self

    def __exit__(self, *exc_info) -> None:
        self.close()

    def _post(self, path: str, payload: dict[str, Any]) -> httpx.Response:
        resp = self._http.post(path, json=payload)
        if resp.status_code >= 400:
            try:
                body = resp.json()
            except ValueError:
                body = resp.text
            # The Go backend's writeError helper always responds {"error": "..."}
            # on failure; surface that message directly instead of the raw dict.
            message = body.get("error", body) if isinstance(body, dict) else body
            raise APIError(resp.status_code, message)
        return resp

    def register(self, hostname: str, crawler_version: str, sources: list[str]) -> None:
        reg = Registration(worker_id=self.worker_id, hostname=hostname,
                            crawler_version=crawler_version, sources=sources)
        self._post("/internal/v1/workers/register", reg.model_dump(mode="json"))

    def heartbeat(self, status: str, current_job_id: str | None = None) -> None:
        req = HeartbeatRequest(status=status, current_job_id=current_job_id)
        self._post(f"/internal/v1/workers/{self.worker_id}/heartbeat", req.model_dump(mode="json"))

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
        resp = self._post(f"/internal/v1/jobs/{job_id}/fail", req.model_dump(mode="json"))
        return Outcome.model_validate(resp.json())
