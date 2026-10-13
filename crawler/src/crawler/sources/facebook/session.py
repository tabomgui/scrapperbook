"""Persistent-context session helpers."""

LOGIN_COOKIE_NAME = "c_user"


def is_logged_in(cookies: list[dict]) -> bool:
    """`cookies` is Playwright's BrowserContext.cookies() result: a list of
    dicts with at least "name" and "value". Returns True if a non-empty
    Facebook session cookie is present.
    """
    return any(c.get("name") == LOGIN_COOKIE_NAME and c.get("value") for c in cookies)
