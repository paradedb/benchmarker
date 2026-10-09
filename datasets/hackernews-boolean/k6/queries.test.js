import assert from "node:assert/strict";
import test from "node:test";
import {
  generateCases,
  leaves,
  request,
  selectCases,
  shuffled,
  tree,
} from "./queries.js";

const catalog = {
  leaves: ["text", "filter"].flatMap((kind) =>
    Array.from({ length: 32 }, (_, i) => ({
      id: `${kind}:${i}`,
      kind,
      field: kind === "text" ? "text" : "score",
      operator: kind === "text" ? "|||" : ">=",
      value: kind === "text" ? `term${i}` : i,
      match_fraction: (i + 1) / 33,
    })),
  ),
};

test("all 72 trees have distinct leaves and expand into exactly 192 requests", () => {
  const cases = generateCases(catalog);
  assert.equal(cases.length, 72);
  assert.equal(new Set(cases.map((query) => query.id)).size, 72);
  assert.equal(
    cases.reduce((sum, query) => sum + query.modes.length, 0),
    192,
  );
  for (const query of cases) {
    assert.equal(leaves(query.predicate).length, 12);
    assert.equal(
      new Set(leaves(query.predicate).map((leaf) => leaf.id)).size,
      12,
    );
    assert.equal(
      leaves(query.predicate).filter((leaf) => leaf.kind === "text").length,
      query.mix === "text_heavy" ? 8 : query.mix === "balanced" ? 6 : 0,
    );
  }
});

test("seeds reproduce the corpus and reorder it without changing its membership", () => {
  assert.deepEqual(generateCases(catalog), generateCases(catalog));
  assert.notDeepEqual(
    generateCases(catalog),
    generateCases(catalog, { seed: 7 }),
  );
  assert.deepEqual(shuffled([1, 2, 3, 4], 9).sort(), [1, 2, 3, 4]);
});

test("each mix/profile uses the same leaf set under every Boolean shape", () => {
  const cases = generateCases(catalog);
  for (const query of cases) {
    const reference = cases.find(
      (other) => other.mix === query.mix && other.profile === query.profile,
    );
    assert.deepEqual(
      leaves(query.predicate)
        .map((leaf) => leaf.id)
        .sort(),
      leaves(reference.predicate)
        .map((leaf) => leaf.id)
        .sort(),
    );
  }
});

test("SQL binds values, preserves Boolean structure and uses only the requested scoring", () => {
  const data = [
    {
      field: "text",
      operator: "|||",
      value: "x' OR true --",
      match_fraction: 0.1,
    },
    { field: "time", operator: ">=", value: "2020-01-01", match_fraction: 0.2 },
    { field: "score", operator: ">=", value: 5, match_fraction: 0.3 },
    { field: "deleted", operator: "=", value: false, match_fraction: 0.9 },
  ];
  const query = {
    predicate: tree("exclude", data),
    modes: ["count", "bm25", "time"],
  };
  const [count, ...params] = request(query, "count");
  assert.match(count, /id @@@ pdb.all\(\) AND/);
  assert.match(count, /\(NOT \(\("score" >= \$3\) OR \("deleted" = \$4\)\)\)/);
  assert.match(count, /\$2::timestamptz/);
  assert.doesNotMatch(count, /x'|ORDER BY|LIMIT|pdb.score/);
  assert.deepEqual(
    params,
    data.map((leaf) => leaf.value),
  );
  assert.match(
    request(query, "bm25")[0],
    /pdb.score\(id\) AS relevance.*ORDER BY relevance DESC LIMIT 10/,
  );
  assert.match(
    request(query, "time", 20)[0],
    /ORDER BY "time" DESC NULLS LAST LIMIT 20/,
  );
  assert.doesNotMatch(request(query, "time")[0], /pdb.score/);
});

test("gate places the most selective leaf outside the union", () => {
  const items = catalog.leaves.slice(0, 5).reverse();
  const predicate = tree("gate", items);
  assert.equal(predicate.op, "and");
  assert.equal(predicate.children[0].leaf.id, "text:0");
  assert.equal(predicate.children[1].op, "or");
});

test("pure filter queries omit BM25; unknown and empty selections fail", () => {
  const manifest = { cases: generateCases(catalog) };
  assert.equal(selectCases(manifest, "bm25").length, 48);
  assert.equal(
    selectCases(manifest, "count", { mix: "filters_only" }).length,
    24,
  );
  assert.throws(
    () => selectCases(manifest, "bm25", { mix: "filters_only" }),
    /No queries/,
  );
  assert.throws(() => selectCases(manifest, "wrong"), /MODE/);
  assert.throws(() => generateCases(catalog, { clauses: 3 }), /clauses/);
  assert.throws(() => request(manifest.cases[0], "time", 0), /TOP_K/);
});

test("zero-match leaves are never used as sparse stand-ins", () => {
  const cases = generateCases({
    leaves: [
      ...catalog.leaves,
      { ...catalog.leaves[0], id: "zero", match_fraction: 0 },
    ],
  });
  assert.ok(
    cases.every((query) =>
      leaves(query.predicate).every((leaf) => leaf.id !== "zero"),
    ),
  );
});

test("text leaves use disjunction only, including multiword inputs", () => {
  const phrase = {
    ...catalog.leaves[0],
    id: "phrase",
    operator: "###",
    value: "open source",
  };
  const cases = generateCases({ leaves: [...catalog.leaves, phrase] });
  assert.ok(
    cases.every((query) =>
      leaves(query.predicate).every(
        (leaf) => leaf.kind !== "text" || leaf.operator === "|||",
      ),
    ),
  );
  const query = {
    predicate: tree("or", [{ ...phrase, operator: "|||" }]),
    modes: ["count"],
  };
  assert.deepEqual(request(query, "count").slice(1), ["open source"]);
  assert.match(request(query, "count")[0], /"text" \|\|\| \$1/);
  assert.throws(
    () => request({ ...query, predicate: tree("or", [phrase]) }, "count"),
    /Unsupported predicate/,
  );
});
