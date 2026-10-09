#!/usr/bin/env python3
"""Freeze measured text disjunctions and sampled HN filter distributions."""

import argparse
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path

from pg import literal, query

TERMS = [
    "the",
    "of",
    "and",
    "to",
    "is",
    "in",
    "that",
    "it",
    "for",
    "you",
    "with",
    "on",
    "rust",
    "python",
    "javascript",
    "database",
    "postgresql",
    "linux",
    "security",
    "startup",
    "compiler",
    "kubernetes",
    "haskell",
    "erlang",
    "postgres",
    "sqlite",
    "algorithm",
    "browser",
]
TEXT_GROUPS = ["open source", "machine learning", "rust python", "database sql"]
FIELDS = [
    "id",
    "time",
    "score",
    "descendants",
    "parent",
    "type",
    "by",
    "dead",
    "deleted",
]


def filter_leaves(rows):
    result = []
    seen = set()

    def add(field, operator, value):
        key = (field, operator, value)
        if key in seen:
            return
        seen.add(key)
        values = [row[field] for row in rows if row[field] is not None]
        if operator == "=":
            matches = sum(item == value for item in values)
        elif operator == ">=":
            matches = sum(item >= value for item in values)
        else:
            matches = sum(item < value for item in values)
        if matches == 0 or matches == len(rows):
            return
        result.append(
            {
                "id": f"filter:{field}:{operator}:{value}",
                "kind": "filter",
                "field": field,
                "operator": operator,
                "value": value,
                "match_fraction": matches / len(rows),
                "measurement": "heap_system_sample",
                "sample_matches": matches,
            }
        )

    for field in ["id", "time", "score", "descendants", "parent"]:
        values = sorted(row[field] for row in rows if row[field] is not None)
        if not values:
            continue
        for fraction in [0.001, 0.01, 0.1, 0.5, 0.9, 0.99, 0.999]:
            value = values[min(len(values) - 1, int(len(values) * fraction))]
            for operator in [">=", "<"]:
                add(field, operator, value)
    for field in ["type", "by", "dead", "deleted"]:
        counts = Counter(row[field] for row in rows if row[field] is not None)
        for value, _ in counts.most_common(10):
            add(field, "=", value)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--output", type=Path, default=Path(__file__).parent / "artifacts/catalog.json"
    )
    parser.add_argument("--sample-percent", type=float, default=0.1)
    parser.add_argument("--seed", type=int, default=20261008)
    args = parser.parse_args()
    if not 0 < args.sample_percent <= 100:
        parser.error("sample-percent must be in (0, 100]")
    fields = ", ".join(f'"{field}"' for field in FIELDS)
    rows = query(
        f"SELECT coalesce(json_agg(s), '[]'::json) FROM (SELECT {fields} FROM hn_items TABLESAMPLE SYSTEM ({args.sample_percent}) REPEATABLE ({args.seed})) s"
    )
    if len(rows) < 100:
        raise ValueError("Too few sampled rows; increase --sample-percent")
    metadata = query("""SELECT json_build_object(
        'postgres_version', version(),
        'extension_version', (SELECT extversion FROM pg_extension WHERE extname='pg_search'),
        'indexes', (SELECT json_agg(indexdef) FROM pg_indexes WHERE tablename='hn_items'),
        'row_count', (SELECT count(*) FROM hn_items))""")
    metadata.update(
        {
            "created_at": datetime.now(timezone.utc).isoformat(),
            "seed": args.seed,
            "sample_percent": args.sample_percent,
            "sample_rows": len(rows),
            "sample_sha256": hashlib.sha256(
                json.dumps(rows, sort_keys=True).encode()
            ).hexdigest(),
            "sample_null_fraction": {
                field: sum(row[field] is None for row in rows) / len(rows)
                for field in FIELDS
            },
            "sample_distinct": {
                field: len({row[field] for row in rows if row[field] is not None})
                for field in FIELDS
            },
        }
    )
    leaves = filter_leaves(rows)
    output = {"version": 1, "metadata": metadata, "leaves": leaves}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    for field in ["text", "title"]:
        for term in TERMS + TEXT_GROUPS:
            operator = "|||"
            count = query(
                f'SELECT count(*) FROM hn_items WHERE "{field}" {operator} {literal(term)}'
            )
            leaves.append(
                {
                    "id": f"text:{field}:{operator}:{term}",
                    "kind": "text",
                    "field": field,
                    "operator": operator,
                    "value": term,
                    "match_fraction": count / metadata["row_count"],
                    "matches": count,
                    "measurement": "exact_index_count",
                }
            )
            args.output.write_text(json.dumps(output, indent=2) + "\n")
            print(f"{field} {operator} {term}: {count} matches", flush=True)
    print(f"Saved {len(leaves)} leaves from {len(rows)} sampled rows to {args.output}")


if __name__ == "__main__":
    main()
