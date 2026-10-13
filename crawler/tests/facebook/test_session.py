from crawler.sources.facebook.session import is_logged_in


def test_detects_a_logged_in_session():
    assert is_logged_in([{"name": "c_user", "value": "123456"}, {"name": "xs", "value": "abc"}])


def test_detects_a_logged_out_session():
    assert not is_logged_in([{"name": "xs", "value": "abc"}])
    assert not is_logged_in([{"name": "c_user", "value": ""}])
    assert not is_logged_in([])
