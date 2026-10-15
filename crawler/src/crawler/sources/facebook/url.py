"""Builds the Facebook Marketplace search URL from a job's search+region payload.

Only max_price/min_price/days_since_listed are forwarded today — the design
spec notes the radius URL param is unreliable while logged in (the server
uses whatever radius was last set in the UI instead), so it's deliberately
not sent here; see design spec §4.3.

Scoped to the Vehicles category (`category_id`) because the plain
cross-category `/search` endpoint interleaves spare-parts/decor listings
with real for-sale vehicles and returns a far smaller, noisier result set
for the same keyword+location — confirmed live (10 items/run, mostly
spare parts, missing known-to-exist vehicle listings) vs. the
category-scoped endpoint (120 items/run, vehicles only) against the same
authenticated session. Without scoping, the crawler's limited per-job
scroll budget gets consumed by irrelevant matches before reaching the
actual cars.
"""

from urllib.parse import urlencode

BASE = "https://www.facebook.com/marketplace"

# Facebook's Marketplace "Carros e caminhonetes" (Vehicles) category id —
# confirmed stable in backend/internal/source/facebook/testdata/listing_node.json.
VEHICLES_CATEGORY_ID = "807311116002614"

_FILTER_PARAM_NAMES = {
    "max_price": "maxPrice",
    "min_price": "minPrice",
    "days_since_listed": "daysSinceListed",
}


def build_search_url(fb_location: str, keyword: str, filters: dict) -> str:
    params = {
        "query": keyword,
        "sortBy": "creation_time_descend",
        "category_id": VEHICLES_CATEGORY_ID,
    }
    for key, param_name in _FILTER_PARAM_NAMES.items():
        if filters.get(key) is not None:
            params[param_name] = str(filters[key])
    return f"{BASE}/{fb_location}/search?{urlencode(params)}"
