"""Regenerate docs/contracts/*.schema.json from the pydantic models.

Run after changing crawler/src/crawler/contracts.py:
    cd crawler && .venv/bin/python scripts/export_contracts.py
"""

import json
from pathlib import Path

from crawler.contracts import ClaimedJob, ResultRequest

REPO_ROOT = Path(__file__).resolve().parents[2]
CONTRACTS_DIR = REPO_ROOT / "docs" / "contracts"


def main() -> None:
    CONTRACTS_DIR.mkdir(parents=True, exist_ok=True)
    (CONTRACTS_DIR / "job.schema.json").write_text(
        json.dumps(ClaimedJob.model_json_schema(), indent=2) + "\n")
    (CONTRACTS_DIR / "result.schema.json").write_text(
        json.dumps(ResultRequest.model_json_schema(), indent=2) + "\n")
    print(f"wrote job.schema.json and result.schema.json to {CONTRACTS_DIR}")


if __name__ == "__main__":
    main()
