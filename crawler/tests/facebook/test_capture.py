import json
from pathlib import Path

from crawler.sources.facebook.capture import parse_graphql_response

FIXTURE = json.loads(
    (Path(__file__).resolve().parents[2] / "testdata" / "graphql_search_response.json").read_text()
)


def test_parses_two_listings_and_skips_malformed_edges():
    # The fixture has 2 real listings plus 2 malformed edges: one missing
    # "listing" entirely, and one with "node": null — a shape real
    # Facebook feeds produce for filtered/removed units. Neither must crash
    # the parse or affect the 2 valid listings.
    items = parse_graphql_response(FIXTURE)
    assert items is not None
    assert len(items) == 2
    assert items[0]["external_id"] == "29168236012774043"
    assert items[0]["raw"]["marketplace_listing_title"] == "2000 Honda Civic"
    assert items[1]["external_id"] == "2270441420418044"


def test_returns_none_for_an_unrelated_graphql_response():
    assert parse_graphql_response({"data": {"viewer": {}}}) is None
    assert parse_graphql_response({}) is None
    assert parse_graphql_response({"data": None}) is None


def test_returns_empty_list_for_zero_results():
    empty = {"data": {"marketplace_search": {"feed_units": {"edges": []}}}}
    assert parse_graphql_response(empty) == []


def test_skips_a_listing_with_a_non_scalar_id():
    body = {"data": {"marketplace_search": {"feed_units": {"edges": [
        {"node": {"listing": {"id": ["not", "a", "scalar"], "marketplace_listing_title": "x"}}},
        {"node": {"listing": {"id": "1", "marketplace_listing_title": "y"}}},
    ]}}}}
    items = parse_graphql_response(body)
    assert len(items) == 1
    assert items[0]["external_id"] == "1"
