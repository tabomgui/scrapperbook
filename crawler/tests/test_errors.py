import pytest

from crawler.errors import CrawlError, ErrorKind


def test_error_kind_values_match_go():
    assert {k.value for k in ErrorKind} == {
        "network", "timeout", "rate_limited",
        "checkpoint", "login_required", "layout_changed",
    }


def test_crawl_error_carries_kind_and_message():
    err = CrawlError(ErrorKind.CHECKPOINT, "hit a checkpoint page")
    assert err.kind is ErrorKind.CHECKPOINT
    assert str(err) == "hit a checkpoint page"


def test_crawl_error_requires_a_valid_kind():
    with pytest.raises(TypeError):
        CrawlError("not-a-kind", "x")
