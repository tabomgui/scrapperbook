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
