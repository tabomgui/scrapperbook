"""Drives Playwright end to end for one search job. Not unit tested — see
the plan's Task 11 note. Verified manually by the human user, and
structurally by `python3 -c "import crawler.sources.facebook.run"`
succeeding (Step 6).
"""

import asyncio

from playwright.async_api import Response, async_playwright

from crawler.contracts import ClaimedJob, ResultItem
from crawler.errors import CrawlError, ErrorKind
from crawler.sources.facebook.capture import parse_graphql_response
from crawler.sources.facebook.constants import SOURCE
from crawler.sources.facebook.detect import classify_status, classify_url
from crawler.sources.facebook.fallback import parse_embedded_json
from crawler.sources.facebook.session import is_logged_in
from crawler.sources.facebook.url import build_search_url


class FacebookAdapter:
    source = SOURCE

    def __init__(self, profile_dir: str, scroll_pause_seconds: float = 3.0):
        self.profile_dir = profile_dir
        self.scroll_pause_seconds = scroll_pause_seconds

    def run(self, job: ClaimedJob) -> list[ResultItem]:
        return asyncio.run(self._run_async(job))

    async def _run_async(self, job: ClaimedJob) -> list[ResultItem]:
        async with async_playwright() as pw:
            context = await pw.chromium.launch_persistent_context(
                self.profile_dir, headless=True, viewport={"width": 1280, "height": 900},
            )
            try:
                return await self._search(context, job)
            finally:
                await context.close()

    async def _search(self, context, job: ClaimedJob) -> list[ResultItem]:
        if not is_logged_in(await context.cookies()):
            raise CrawlError(ErrorKind.LOGIN_REQUIRED, "no active facebook session in the browser profile")

        captured: list[dict] = []
        # True once we've seen at least one correctly-shaped GraphQL search
        # response, even if it carried zero listings — distinguishes "ran a
        # real search that genuinely matched nothing" (success, per design
        # spec §4.4 "0 resultados não é erro") from "never managed to parse
        # anything, something's broken" (CrawlError.LAYOUT_CHANGED below).
        saw_valid_response = False
        # page.on("response", ...) callbacks run as fire-and-forget asyncio
        # Tasks (Playwright has no native async-listener support) — nothing
        # awaits on_response directly, so a CrawlError raised inside it would
        # never reach this method's await chain, it would just be logged by
        # asyncio as "exception was never retrieved" and silently lost. Stash
        # it here instead, and check it at every point this method already
        # checks for a checkpoint redirect.
        pending_error: CrawlError | None = None
        page = await context.new_page()

        async def on_response(response: Response) -> None:
            nonlocal saw_valid_response, pending_error
            if "/api/graphql/" not in response.url:
                return
            status_kind = classify_status(response.status)
            if status_kind is not None:
                pending_error = CrawlError(status_kind, f"http {response.status} on {response.url}")
                return
            try:
                body = await response.json()
            except Exception:  # noqa: BLE001 - not every graphql call returns JSON we care about
                return
            items = parse_graphql_response(body)
            if items is None:
                return  # wrong shape, not a search response — not evidence either way
            saw_valid_response = True
            if items:
                captured.extend(items)

        page.on("response", lambda r: asyncio.ensure_future(on_response(r)))

        url = build_search_url(job.region.fb_location, job.search.keyword, job.search.filters)
        await page.goto(url, wait_until="domcontentloaded")

        # pending_error (rate limit/network, from a response) is checked
        # before the checkpoint redirect (from the page URL) at both points
        # below. If both are true on the same tick, pending_error wins and
        # the checkpoint is only caught on a later retry's goto/scroll check
        # — a one-attempt delay in surfacing the more serious signal, not
        # data loss, since classify_url is stateless and re-evaluated every
        # time. Not fixed on purpose: no evidence yet that this ordering
        # matters in practice, and guessing at a "right" priority without a
        # real occurrence to go by risks getting it wrong in some other way.
        if pending_error is not None:
            raise pending_error
        checkpoint = classify_url(page.url)
        if checkpoint is not None:
            raise CrawlError(checkpoint, f"redirected to {page.url}")

        if not captured and not saw_valid_response:
            fallback_items = parse_embedded_json(await page.content())
            if fallback_items:
                captured.extend(fallback_items)
                saw_valid_response = True

        pages_scrolled = 0
        while pages_scrolled < job.limits.max_pages and len(captured) < job.limits.max_items:
            await page.mouse.wheel(0, 2000)
            await page.wait_for_timeout(int(self.scroll_pause_seconds * 1000))
            pages_scrolled += 1
            if pending_error is not None:
                raise pending_error
            checkpoint = classify_url(page.url)
            if checkpoint is not None:
                raise CrawlError(checkpoint, f"redirected to {page.url} mid-scroll")

        if not captured and not saw_valid_response:
            raise CrawlError(ErrorKind.LAYOUT_CHANGED, "no listings captured from graphql or embedded json")

        seen_external_ids: set[str] = set()
        items: list[ResultItem] = []
        for raw in captured:
            if raw["external_id"] in seen_external_ids:
                continue
            seen_external_ids.add(raw["external_id"])
            items.append(ResultItem(external_id=raw["external_id"], raw=raw["raw"]))
            if len(items) >= job.limits.max_items:
                break
        return items
