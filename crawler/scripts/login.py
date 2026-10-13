"""Manual login entrypoint. Run this once (and again whenever the session
expires) inside the crawler container with WORKER_MODE=login. It opens a
real, visible Chromium window (visible via noVNC, not headless) pointed at
Facebook's login page, and waits for you to log in by hand. Never attempts
to fill in credentials, solve a CAPTCHA, or complete 2FA automatically.
"""

import asyncio
import os
import sys

from playwright.async_api import async_playwright

from crawler.sources.facebook.session import is_logged_in

PROFILE_DIR = os.environ.get("PROFILE_DIR", "/data/profile")
POLL_SECONDS = 2
TIMEOUT_SECONDS = 15 * 60


async def main() -> None:
    print(f"Opening a browser against profile {PROFILE_DIR!r}. Log in by hand; "
          f"this script exits automatically once it detects a session cookie, "
          f"or after {TIMEOUT_SECONDS // 60} minutes.")
    async with async_playwright() as pw:
        context = await pw.chromium.launch_persistent_context(
            PROFILE_DIR, headless=False, viewport={"width": 1280, "height": 900},
        )
        try:
            page = await context.new_page()
            await page.goto("https://www.facebook.com/login")
            waited = 0
            while waited < TIMEOUT_SECONDS:
                if is_logged_in(await context.cookies()):
                    print("Logged in. Session saved to the profile directory.")
                    return
                await asyncio.sleep(POLL_SECONDS)
                waited += POLL_SECONDS
            print("Timed out waiting for login.", file=sys.stderr)
            sys.exit(1)
        finally:
            await context.close()


if __name__ == "__main__":
    asyncio.run(main())
