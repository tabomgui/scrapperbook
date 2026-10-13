"""Falls back to the JSON Facebook embeds directly in the search page's
HTML, for when no GraphQL response was captured (e.g. the first page of
results, server-rendered before any XHR fires).
"""

import json
import re
from typing import Any

from crawler.sources.facebook.capture import parse_graphql_response

# Non-greedy match ends at the first literal "</script>" after the opening
# tag. This looks fragile for a JSON blob that might contain that substring
# inside a string value (e.g. a listing description), but it isn't actually
# weaker than a real HTML parser here: per the HTML5 spec, a browser's own
# <script> raw-text tokenizer stops at the same byte sequence, so Facebook's
# own serializer must already escape "</" inside these blobs (typically as
# "<\/") or its page would render broken HTML for every real visitor, not
# just this crawler. A proper HTML parser would face the identical
# constraint, so this isn't a case where swapping tools buys more safety.
_SCRIPT_RE = re.compile(
    r'<script[^>]*type="application/json"[^>]*>(.*?)</script>', re.DOTALL
)


def parse_embedded_json(html: str) -> list[dict[str, Any]]:
    """Scans every `<script type="application/json">` tag for one that,
    anywhere in its structure, contains a "marketplace_search" key — the
    page embeds this inside a deeply nested, version-specific wrapper that
    isn't worth modeling exactly, so this searches for the key by shape
    instead of by path.

    Returns the FIRST non-empty match across all script tags. This is an
    untested assumption: if a real page ever embeds more than one
    marketplace_search-shaped blob (e.g. a "related searches" widget using
    the same field name), this could return the wrong one. There was no
    real Facebook page available to verify this against when writing this
    function — the manual fixture-capture task (see the implementation
    plan) should confirm this assumption holds, or fix the tie-break here,
    once a real page is available to inspect.
    """
    for match in _SCRIPT_RE.finditer(html):
        try:
            data = json.loads(match.group(1))
        except json.JSONDecodeError:
            continue
        found = _find_marketplace_search(data)
        if found is None:
            continue
        items = parse_graphql_response({"data": {"marketplace_search": found}})
        if items:
            return items
    return []


def _find_marketplace_search(obj: Any) -> dict | None:
    if isinstance(obj, dict):
        candidate = obj.get("marketplace_search")
        if isinstance(candidate, dict):
            return candidate
        for value in obj.values():
            found = _find_marketplace_search(value)
            if found is not None:
                return found
    elif isinstance(obj, list):
        for value in obj:
            found = _find_marketplace_search(value)
            if found is not None:
                return found
    return None
