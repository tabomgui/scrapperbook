import pytest

from crawler.config import ConfigError, Settings


def env(values: dict) -> callable:
    return values.get


def test_defaults():
    s = Settings.load(env({"API_BASE_URL": "http://api:8080", "WORKER_TOKEN": "t"}))
    assert s.api_base_url == "http://api:8080"
    assert s.worker_token == "t"
    assert s.sources == ["facebook_marketplace"]
    assert s.types == ["search"]
    assert s.poll_interval_seconds == 10
    assert s.crawler_version == "0.1.0"
    assert s.profile_dir == "/data/profile"
    assert s.worker_id  # non-empty, derived from hostname when not set


def test_overrides():
    s = Settings.load(env({
        "API_BASE_URL": "http://api:8080", "WORKER_TOKEN": "t",
        "WORKER_ID": "custom-1", "SOURCES": "facebook_marketplace,olx",
        "TYPES": "search,enrich", "POLL_INTERVAL_SECONDS": "30",
        "PROFILE_DIR": "/custom/profile",
    }))
    assert s.worker_id == "custom-1"
    assert s.sources == ["facebook_marketplace", "olx"]
    assert s.types == ["search", "enrich"]
    assert s.poll_interval_seconds == 30
    assert s.profile_dir == "/custom/profile"


def test_missing_required_vars_raise():
    with pytest.raises(ConfigError):
        Settings.load(env({"WORKER_TOKEN": "t"}))
    with pytest.raises(ConfigError):
        Settings.load(env({"API_BASE_URL": "http://api:8080"}))


def test_bad_poll_interval_raises():
    with pytest.raises(ConfigError):
        Settings.load(env({"API_BASE_URL": "http://api:8080", "WORKER_TOKEN": "t",
                            "POLL_INTERVAL_SECONDS": "soon"}))


def test_non_positive_poll_interval_raises():
    for bad in ("0", "-5"):
        with pytest.raises(ConfigError):
            Settings.load(env({"API_BASE_URL": "http://api:8080", "WORKER_TOKEN": "t",
                                "POLL_INTERVAL_SECONDS": bad}))
