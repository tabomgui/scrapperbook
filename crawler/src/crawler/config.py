"""Reads worker settings from environment variables.

Mirrors backend/internal/config's injected-getenv pattern for the same
reason: testable without os.environ side effects.
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
        if poll_interval <= 0:
            # 0 wouldn't error anywhere downstream, it would turn the worker's
            # poll loop into a busy-loop with no backoff between claim calls.
            raise ConfigError(f"POLL_INTERVAL_SECONDS: must be positive, got {poll_interval}")

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
