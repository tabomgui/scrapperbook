from crawler.errors import ErrorKind
from crawler.sources.facebook.detect import classify_status, classify_url


def test_classify_url_detects_checkpoint():
    assert classify_url("https://www.facebook.com/checkpoint/1501092823525282/") is ErrorKind.CHECKPOINT


def test_classify_url_detects_two_step_verification():
    assert classify_url("https://www.facebook.com/two_step_verification/authentication/") is ErrorKind.CHECKPOINT


def test_classify_url_detects_login_wall():
    assert classify_url("https://www.facebook.com/login/?next=%2Fmarketplace%2F") is ErrorKind.LOGIN_REQUIRED


def test_classify_url_returns_none_for_a_normal_search_url():
    assert classify_url("https://www.facebook.com/marketplace/curitiba/search?query=civic") is None


def test_classify_status_detects_rate_limiting():
    assert classify_status(429) is ErrorKind.RATE_LIMITED


def test_classify_status_treats_server_errors_as_network():
    assert classify_status(500) is ErrorKind.NETWORK
    assert classify_status(503) is ErrorKind.NETWORK


def test_classify_status_returns_none_for_success():
    assert classify_status(200) is None
    assert classify_status(304) is None


def test_classify_url_does_not_false_positive_on_a_similar_looking_path():
    # These paths merely contain the marker words as a prefix of a longer
    # segment — none of them are an actual login wall or checkpoint.
    assert classify_url("https://www.facebook.com/marketplace/profile/logintips2024/") is None
    assert classify_url("https://www.facebook.com/marketplace/category/recovered-electronics/") is None
    assert classify_url("https://www.facebook.com/checkpointer-vintage-store/") is None
