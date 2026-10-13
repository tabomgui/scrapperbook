from pathlib import Path

from crawler.sources.facebook.fallback import parse_embedded_json

FIXTURE_HTML = (
    Path(__file__).resolve().parents[2] / "testdata" / "embedded_page.html"
).read_text()


def test_finds_the_listing_buried_in_an_unrelated_json_blob():
    items = parse_embedded_json(FIXTURE_HTML)
    assert len(items) == 1
    assert items[0]["external_id"] == "29168236012774043"
    assert items[0]["raw"]["marketplace_listing_title"] == "2000 Honda Civic"


def test_returns_empty_list_when_nothing_matches():
    assert parse_embedded_json("<html><body>no scripts here</body></html>") == []
    assert parse_embedded_json(
        '<script type="application/json">{"not": "json-that-matters"}</script>'
    ) == []


def test_tolerates_malformed_json_in_one_script_tag():
    html = (
        '<script type="application/json">{not valid json</script>'
        '<script type="application/json">'
        '{"data":{"marketplace_search":{"feed_units":{"edges":'
        '[{"node":{"listing":{"id":"1","marketplace_listing_title":"Civic"}}}]}}}}'
        "</script>"
    )
    items = parse_embedded_json(html)
    assert len(items) == 1
    assert items[0]["external_id"] == "1"
