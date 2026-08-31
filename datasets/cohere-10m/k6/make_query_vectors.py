#!/usr/bin/env python3
"""Convert the Cohere query embeddings parquet into query_vectors.json.

The output is a JSON array of pgvector literals ("[0.1,0.2,...]") that
k6 scripts can serve through db.terms().
"""

import argparse
import json

import pyarrow.parquet as pq


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data", default="../queries/cohere_queries.parquet")
    parser.add_argument("--column", default="emb")
    parser.add_argument("--out", default="query_vectors.json")
    args = parser.parse_args()

    embeddings = pq.read_table(args.data, columns=[args.column])
    vectors = [
        "[" + ",".join(repr(float(x)) for x in embedding) + "]"
        for embedding in embeddings.column(args.column).to_pylist()
    ]
    with open(args.out, "w") as f:
        json.dump(vectors, f)
    print(f"Wrote {len(vectors)} query vectors to {args.out}")


if __name__ == "__main__":
    main()
