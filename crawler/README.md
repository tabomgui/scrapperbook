# crawler

Python worker for the marketplace monitor's Go backend (`../backend`).

## Dev setup

    cd crawler
    python3 -m venv .venv
    source .venv/bin/activate
    pip install -e ".[dev]"

## Tests

    pytest -v

No test here touches a real browser or Facebook — see the design spec's
"Why no Playwright on the host" note in the implementation plan.
