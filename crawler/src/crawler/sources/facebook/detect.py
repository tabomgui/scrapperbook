"""Classifies what the browser is showing, or what an HTTP response status
was, into the ErrorKind the Go backend's reaction table understands.
"""

from urllib.parse import urlsplit

from crawler.errors import ErrorKind

_CHECKPOINT_SEGMENTS = ("checkpoint", "two_step_verification", "recover")
_LOGIN_SEGMENTS = ("login",)


def classify_url(url: str) -> ErrorKind | None:
    # Match whole path segments, not a raw substring: "/login" must not
    # match "/logintips2024" the way `"/login" in path` would. This matters
    # because a false positive here pauses the entire source until manual
    # relogin (see the backend's reaction table) — an expensive mistake to
    # make on a normal marketplace/profile/category path that merely starts
    # with one of these words.
    segments = urlsplit(url).path.split("/")
    if any(marker in segments for marker in _CHECKPOINT_SEGMENTS):
        return ErrorKind.CHECKPOINT
    if any(marker in segments for marker in _LOGIN_SEGMENTS):
        return ErrorKind.LOGIN_REQUIRED
    return None


def classify_status(status: int) -> ErrorKind | None:
    if status == 429:
        return ErrorKind.RATE_LIMITED
    if status >= 500:
        return ErrorKind.NETWORK
    return None
