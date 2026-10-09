#!/usr/bin/env python3
"""Save every request's plan and result; reject execution outside ParadeDB."""

import argparse
import json
from pathlib import Path
import time

from pg import bind, nodes, query


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("manifest", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--ready-output", type=Path)
    parser.add_argument("--limit", type=int, default=0)
    parser.add_argument("--timeout", type=int, default=120)
    args = parser.parse_args()
    manifest = json.loads(args.manifest.read_text())
    output = args.output or args.manifest.with_name("validation.json")
    report = {"manifest": args.manifest.name, "requests": [], "errors": []}
    ready = []
    cases = manifest["cases"][: args.limit or None]
    for case in cases:
        ready_modes = []
        for mode, request in case["requests"].items():
            key = f"{case['id']}:{mode}" + ("" if mode == "count" else ":k10")
            plan = None
            try:
                sql = bind(request)
                plan = query(f"EXPLAIN (FORMAT JSON) {sql}", args.timeout)
                all_nodes = list(nodes(plan[0]["Plan"]))
                planner_empty = all(
                    node.get("Node Type") in ["Result", "Limit", "Sort", "Aggregate"]
                    for node in all_nodes
                ) and any(node.get("One-Time Filter") == "false" for node in all_nodes)
                custom = [
                    node
                    for node in all_nodes
                    if "paradedb"
                    in json.dumps(node.get("Custom Plan Provider", "")).lower()
                ]
                if not custom and not planner_empty:
                    raise ValueError("No ParadeDB custom scan")
                if not planner_empty and any(
                    node.get("Node Type") in ["Seq Scan", "Sort", "Incremental Sort"]
                    for node in all_nodes
                ):
                    raise ValueError("Heap scan or external sort in plan")
                if any(node.get("Filter") for node in all_nodes):
                    raise ValueError("Residual PostgreSQL filter in plan")
                if (
                    not planner_empty
                    and mode == "count"
                    and any(node.get("Node Type") == "Aggregate" for node in all_nodes)
                ):
                    raise ValueError("Count is not pushed into ParadeDB")
                started = time.monotonic()
                result = query(
                    f"SELECT coalesce(json_agg(r), '[]'::json) FROM ({sql}) r",
                    args.timeout,
                )
                record = {
                    "id": key,
                    "mode": mode,
                    "status": "planner_empty" if planner_empty else "pushed_down",
                    "plan": plan,
                    "result": result,
                    "elapsed_ms": (time.monotonic() - started) * 1000,
                }
                if mode == "count":
                    record["matches"] = result[0]["count"]
                    record["match_fraction"] = (
                        result[0]["count"] / manifest["calibration"]["row_count"]
                    )
                report["requests"].append(record)
                if not planner_empty:
                    ready_modes.append(mode)
                print(key, "ok", flush=True)
            except Exception as error:
                report["errors"].append({"id": key, "error": str(error), "plan": plan})
                print(key, str(error), flush=True)
            output.write_text(json.dumps(report, indent=2) + "\n")
        if ready_modes:
            ready.append(
                {
                    **case,
                    "modes": ready_modes,
                    "requests": {mode: case["requests"][mode] for mode in ready_modes},
                }
            )
    if not report["errors"]:
        ready_output = args.ready_output or args.manifest.with_name(
            "validated-queries.json"
        )
        ready_output.write_text(
            json.dumps({**manifest, "cases": ready}, indent=2) + "\n"
        )
    print(f"{len(report['requests'])} passed; {len(report['errors'])} failed")
    raise SystemExit(bool(report["errors"]))


if __name__ == "__main__":
    main()
