"""Builds the Facebook Marketplace search URL from a job's search+region payload.

Only max_price/min_price/days_since_listed are forwarded today — the design
spec notes the radius URL param is unreliable while logged in (the server
uses whatever radius was last set in the UI instead), so it's deliberately
not sent here; see design spec §4.3.
"""

from urllib.parse import urlencode

BASE = "https://www.facebook.com/marketplace"

_FILTER_PARAM_NAMES = {
    "max_price": "maxPrice",
    "min_price": "minPrice",
    "days_since_listed": "daysSinceListed",
}


def build_search_url(fb_location: str, keyword: str, filters: dict) -> str:
    params = {"query": keyword, "sortBy": "creation_time_descend"}
    for key, param_name in _FILTER_PARAM_NAMES.items():
        if filters.get(key) is not None:
            params[param_name] = str(filters[key])
    return f"{BASE}/{fb_location}/search?{urlencode(params)}"
