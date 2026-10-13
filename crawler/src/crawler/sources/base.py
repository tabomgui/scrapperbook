"""The interface every source adapter implements."""

from typing import Protocol

from crawler.contracts import ClaimedJob, ResultItem


class Adapter(Protocol):
    source: str

    def run(self, job: ClaimedJob) -> list[ResultItem]:
        """Execute one job and return the raw items found.

        An empty list means zero results, which is success, not failure
        (see design spec §4.4: "0 resultados não é erro"). Raise
        crawler.errors.CrawlError for a classified failure, or let any
        other exception propagate — the worker loop treats an
        unclassified exception as ErrorKind.NETWORK.
        """
        ...
