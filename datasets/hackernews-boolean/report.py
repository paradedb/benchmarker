#!/usr/bin/env python3
"""Check query coverage and compare per-query CSVs from the same frozen workload."""

import argparse
import csv
import json
from pathlib import Path
from statistics import median


def read_csv(path):
    with path.open() as stream:
        rows = list(csv.DictReader(stream))
    ids = [row["query_id"] for row in rows]
    if len(set(ids)) != len(ids):
        raise ValueError("Use a CSV with one run/backend per query ID")
    return {row["query_id"]: row for row in rows}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("manifest", type=Path)
    parser.add_argument("csv", type=Path)
    parser.add_argument("--mode", required=True, choices=["count", "bm25", "time"])
    parser.add_argument("--baseline", type=Path)
    parser.add_argument("--min-samples", type=int, default=3)
    parser.add_argument("--top-k", type=int, default=10)
    for option in ["shape", "mix", "profile"]:
        parser.add_argument(f"--{option}")
    args = parser.parse_args()
    manifest = json.loads(args.manifest.read_text())
    suffix = "" if args.mode == "count" else f":k{args.top_k}"
    expected = {
        f"{case['id']}:{args.mode}{suffix}": case
        for case in manifest["cases"]
        if args.mode in case["modes"]
        and all(
            not getattr(args, key) or case[key] == getattr(args, key)
            for key in ["shape", "mix", "profile"]
        )
    }
    current = read_csv(args.csv)
    missing = sorted(expected.keys() - current.keys())
    short = sorted(
        key
        for key in expected.keys() & current.keys()
        if int(current[key]["count"]) < args.min_samples
    )
    print(
        json.dumps(
            {
                "expected": len(expected),
                "observed": len(current),
                "missing": missing,
                "undersampled": short,
            },
            indent=2,
        )
    )
    if args.baseline:
        baseline = read_csv(args.baseline)
        missing += sorted(expected.keys() - baseline.keys())
        short += sorted(
            key
            for key in expected.keys() & baseline.keys()
            if int(baseline[key]["count"]) < args.min_samples
        )
        if missing or short:
            raise SystemExit(
                "Comparison has missing or undersampled queries; check corpus hash, filters, and TOP_K"
            )
        changes = []
        for key in expected.keys() & current.keys() & baseline.keys():
            before, after = (
                float(baseline[key]["p50_ms"]),
                float(current[key]["p50_ms"]),
            )
            if (
                before > 0
                and min(int(current[key]["count"]), int(baseline[key]["count"]))
                >= args.min_samples
            ):
                changes.append(
                    {
                        "id": key,
                        "before_ms": before,
                        "after_ms": after,
                        "change_percent": (after / before - 1) * 100,
                    }
                )
        changes.sort(key=lambda row: row["change_percent"], reverse=True)
        print(
            json.dumps(
                {
                    "median_query_change_percent": median(
                        row["change_percent"] for row in changes
                    )
                    if changes
                    else None,
                    "queries": changes,
                },
                indent=2,
            )
        )
    raise SystemExit(bool(missing or short or not expected))


if __name__ == "__main__":
    main()
