#!/usr/bin/env python3
"""Prepare corpus-free S3 config packages for review; never upload or run them.

Requires boto3 and PyYAML. Repository files take precedence over old S3 configs.
Only tracked repository configuration is copied; local untracked work is excluded.
"""

import argparse
import json
import subprocess
from pathlib import Path

import boto3
import yaml

REPO = Path(__file__).resolve().parent.parent
SOURCES = {
    "cohere-1m": "cohere-1m",
    "cohere-10m": "cohere-10m-new",
    "sift-10m": None,
    "sift-100m": None,
    "stackoverflow-100k": "stackoverflow/100k",
    "stackoverflow-1m": "stackoverflow/1m",
    "stackexchange": "stackexchange",
    "wikipedia": "wikipedia",
    "hn_cutdown": "hn_cutdown",
}


def config_file(path):
    parts = Path(path).parts
    if Path(path).is_absolute() or any(part in {".", ".."} for part in parts) or "\\" in path:
        raise ValueError(f"unsafe config key: {path}")
    if len(parts) == 1:
        return path in {"README.md", "schema.yaml", "docker-compose.yml", "download.yaml"}
    return len(parts) == 2 and parts[0] not in {"data", "queries", "fixtures"} and (
        parts[1] in {"pre.sql", "post.sql", "pre.json", "post.json", "backend.yaml"}
        or (parts[0] == "k6" and Path(path).suffix in {".js", ".py", ".json", ".md"})
    )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", default="ci")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    # An exclusive output directory prevents overwriting an earlier review.
    args.output.mkdir(parents=True, exist_ok=False)
    s3 = boto3.Session(profile_name=args.profile).client("s3")
    tracked = subprocess.check_output(["git", "ls-files", "-z", "datasets"], cwd=REPO).decode().split("\0")
    for name, source in SOURCES.items():
        package = args.output / name
        package.mkdir()
        if source:
            prefix = f"old/paradedb-benchmarker/datasets/{source}/"
            pages = s3.get_paginator("list_objects_v2").paginate(Bucket="paradedb-benchmark", Prefix=prefix)
            for page in pages:
                for obj in page.get("Contents", []):
                    relative = obj["Key"][len(prefix):]
                    if not config_file(relative):
                        continue
                    if obj["Size"] > 8 * 1024 * 1024:
                        raise ValueError(f"unexpected large config asset: {obj['Key']}")
                    target = package / relative
                    target.parent.mkdir(parents=True, exist_ok=True)
                    target.write_bytes(s3.get_object(Bucket="paradedb-benchmark", Key=obj["Key"])["Body"].read())
        if name == "cohere-10m":
            # Retain the newer package's extra backends, using current names.
            for old, new in [("vectorsearch", "pg_vectorsearch"), ("pgvector", "postgres")]:
                directory = package / old
                if directory.exists():
                    directory.rename(package / new)
            for path in (package / "k6").iterdir():
                if path.suffix not in {".js", ".py"}:
                    continue
                text = path.read_text().replace("vectorsearch", "pg_vectorsearch").replace("VECTORSEARCH", "PG_VECTORSEARCH")
                # Imported recall scripts had 1M defaults despite the package name.
                text = text.replace("ground_truth_top10_1m.json", "ground_truth_top10_10m.json")
                path.write_text(text)
                if "vectorsearch" in path.name:
                    path.rename(path.with_name(path.name.replace("vectorsearch", "pg_vectorsearch")))
            newer_compose = yaml.safe_load((package / "docker-compose.yml").read_text())
        for filename in tracked:
            prefix = f"datasets/{name}/"
            if not filename.startswith(prefix) or not config_file(filename[len(prefix):]):
                continue
            source_path = REPO / filename
            if source_path.is_file():
                target = package / filename[len(prefix):]
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(source_path.read_bytes())
        if name == "cohere-10m":
            compose = yaml.safe_load((package / "docker-compose.yml").read_text())
            # Keep repo services/settings, add newer package services absent there.
            for old, new in [("vectorsearch", "pg_vectorsearch"), ("vectorchord", "vectorchord")]:
                service = newer_compose["services"][old]
                serialized = json.dumps(service).replace("vectorsearch", "pg_vectorsearch")
                compose["services"].setdefault(new, json.loads(serialized))
            compose.setdefault("volumes", {}).update({"pg_vectorsearch_data": None, "vectorchord_data": None})
            (package / "docker-compose.yml").write_text(yaml.safe_dump(compose, sort_keys=False))
        manifest = REPO / "datasets" / name / "download.yaml"
        if not manifest.exists():
            manifest = REPO / "dataset-packages" / name / "download.yaml"
        (package / "download.yaml").write_bytes(manifest.read_bytes())
        if name in {"stackoverflow-100k", "stackoverflow-1m", "stackexchange", "wikipedia", "hn_cutdown"}:
            readme = package / "README.md"
            original = readme.read_text() if readme.exists() else ""
            if name.startswith("stackoverflow-"):
                original = original.replace("--dataset stackoverflow", f"--dataset {name}")
                original = original.replace("datasets/stackoverflow/1m/", f"benchmarker/{name}/")
                original = original.replace("datasets/stackoverflow", f"datasets/{name}")
                original = original.replace("s3://paradedb-benchmarker/", "s3://paradedb-benchmark/")
            note = (
                f"# {name}: shared corpus package\n\n"
                "Corpus is downloaded into `data/` using `download.yaml`. "
                "The configuration package contains no corpus copies.\n\n"
                f"```bash\nAWS_PROFILE=ci ./bin/loader pull --dataset {name} "
                f"--source s3://paradedb-benchmark/benchmarker/{name}/\n```\n\n"
                "Use an absent or empty destination. Existing nonempty asset "
                "directories are never overwritten. Pulling executes no scripts.\n\n"
            )
            if name == "stackexchange":
                note += "Raise `--max-extracted-bytes` above the 100 GiB default if needed for the 150M-row CSV.\n\n"
            if name == "hn_cutdown":
                note += "This is the 1M text corpus (`id,text`), not the full HN archive or vector dataset.\n\n"
            readme.write_text(note + original.replace("# ", "## ", 1))
        if not (package / "schema.yaml").exists():
            raise ValueError(f"missing schema for {name}")
        print(f"Prepared {name}: {sum(p.is_file() for p in package.rglob('*'))} config files")


if __name__ == "__main__":
    main()
