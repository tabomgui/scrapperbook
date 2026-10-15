from crawler.sources.facebook.url import build_search_url


def test_basic_search_url():
    url = build_search_url(fb_location="curitiba", keyword="Honda Civic EG", filters={})
    assert url == (
        "https://www.facebook.com/marketplace/curitiba/search"
        "?query=Honda+Civic+EG&sortBy=creation_time_descend&category_id=807311116002614"
    )


def test_search_url_scopes_to_vehicles_category():
    url = build_search_url(fb_location="curitiba", keyword="x", filters={})
    assert "category_id=807311116002614" in url


def test_search_url_includes_price_and_days_filters():
    url = build_search_url(
        fb_location="curitiba", keyword="Honda Civic EG",
        filters={"max_price": 30000, "min_price": 10000, "days_since_listed": 7},
    )
    assert "maxPrice=30000" in url
    assert "minPrice=10000" in url
    assert "daysSinceListed=7" in url


def test_search_url_ignores_unknown_filter_keys():
    url = build_search_url(fb_location="curitiba", keyword="x", filters={"condition": "used"})
    assert "condition" not in url


def test_search_url_includes_zero_max_price():
    # max_price=0 ("free only") must survive — this pins down that the
    # implementation checks `is not None`, not truthiness, so a future
    # "simplification" to `if filters.get(key):` would be caught here.
    url = build_search_url(fb_location="curitiba", keyword="x", filters={"max_price": 0})
    assert "maxPrice=0" in url
