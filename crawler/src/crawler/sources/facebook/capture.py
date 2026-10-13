"""Extracts listing nodes from a Facebook Marketplace search GraphQL response.

The response is matched by *shape* (data.marketplace_search.feed_units.edges),
not by doc_id — doc_ids rotate when Facebook deploys, the shape is more
stable (see design spec §4.3/§19).
"""

from typing import Any


def parse_graphql_response(body: dict) -> list[dict[str, Any]] | None:
    """Returns a list of {"external_id", "raw"} dicts, or None if `body`
    isn't a marketplace search response at all (wrong shape — the caller
    should try the next extraction strategy, e.g. the embedded-JSON
    fallback). An empty list means a real response with zero listings.
    """
    try:
        edges = body["data"]["marketplace_search"]["feed_units"]["edges"]
    except (KeyError, TypeError):
        return None
    if not isinstance(edges, list):
        return None

    items: list[dict[str, Any]] = []
    for edge in edges:
        if not isinstance(edge, dict):
            continue
        node = edge.get("node")
        if not isinstance(node, dict):
            continue
        listing = node.get("listing")
        if not isinstance(listing, dict):
            continue
        listing_id = listing.get("id")
        if not isinstance(listing_id, (str, int)):
            continue
        items.append({"external_id": str(listing_id), "raw": listing})
    return items
