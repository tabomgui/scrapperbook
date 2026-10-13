"""Command: registers this worker and runs the claim/run/submit loop forever."""

import os
import socket
import time

from crawler.api import APIClient
from crawler.config import Settings
from crawler.sources.facebook.run import FacebookAdapter
from crawler.worker import run_forever

# depends_on: api only waits for the container to start, not for the Go
# server inside it to be accepting connections — register() can briefly
# fail on a cold start. Retry a few times before giving up, rather than
# crashing the whole container (and the Xvfb/x11vnc/websockify it already
# started) over a race that resolves itself in a second or two.
REGISTER_RETRIES = 5
REGISTER_RETRY_DELAY_SECONDS = 3


def register_with_retry(api: APIClient, settings: Settings) -> None:
    for attempt in range(1, REGISTER_RETRIES + 1):
        try:
            api.register(hostname=socket.gethostname(), crawler_version=settings.crawler_version,
                          sources=settings.sources)
            return
        except Exception as e:  # noqa: BLE001 - any connection error here is worth retrying the same way
            if attempt == REGISTER_RETRIES:
                raise
            print(f"register attempt {attempt}/{REGISTER_RETRIES} failed ({e}); retrying...")
            time.sleep(REGISTER_RETRY_DELAY_SECONDS)


def main() -> None:
    settings = Settings.load(os.environ.get)
    api = APIClient(base_url=settings.api_base_url, token=settings.worker_token,
                     worker_id=settings.worker_id)
    register_with_retry(api, settings)
    adapters = {"facebook_marketplace": FacebookAdapter(profile_dir=settings.profile_dir)}
    print(f"worker {settings.worker_id!r} registered, polling for {settings.sources} jobs...")
    run_forever(
        api, adapters, sources=settings.sources, types=settings.types,
        crawler_version=settings.crawler_version, parser_version="fb-1",
        poll_interval_seconds=settings.poll_interval_seconds,
    )


if __name__ == "__main__":
    main()
