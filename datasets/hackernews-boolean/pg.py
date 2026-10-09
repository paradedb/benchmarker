"""Read-only psql helpers shared by workload preparation and validation."""

import json
import os
import re
import subprocess


def literal(value):
    if value is None:
        return "NULL"
    if isinstance(value, bool):
        return "TRUE" if value else "FALSE"
    if isinstance(value, (int, float)):
        return str(value)
    return "'" + str(value).replace("'", "''") + "'"


def bind(request):
    sql, *params = request
    return re.sub(r"\$(\d+)\b", lambda match: literal(params[int(match[1]) - 1]), sql)


def query(sql, timeout=120):
    env = dict(os.environ)
    if "PARADEDB_URL" not in env:
        raise ValueError("PARADEDB_URL is required")
    env["PGOPTIONS"] = env.get("PGOPTIONS", "") + (
        f" -c default_transaction_read_only=on -c statement_timeout={timeout * 1000}"
        " -c max_parallel_workers_per_gather=0 -c jit=off -c timezone=UTC"
        " -c standard_conforming_strings=on"
    )
    result = subprocess.run(
        ["psql", "-d", env["PARADEDB_URL"], "-X", "-qAt", "-v", "ON_ERROR_STOP=1"],
        input=sql + ";\n",
        text=True,
        capture_output=True,
        env=env,
        timeout=timeout + 10,
    )
    if result.returncode:
        raise RuntimeError(result.stderr.strip())
    return json.loads(result.stdout)


def nodes(plan):
    yield plan
    for child in plan.get("Plans", []):
        yield from nodes(child)
