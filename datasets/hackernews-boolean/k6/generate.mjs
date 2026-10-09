import { readFileSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { generateCases, request } from "./queries.js";

const [input, output, seed = "20261008", clauses = "12"] =
  process.argv.slice(2);
if (!input || !output)
  throw new Error(
    "Usage: node generate.mjs catalog.json queries.json [seed] [clauses]",
  );
const catalog = JSON.parse(readFileSync(input, "utf8"));
const cases = generateCases(catalog, {
  seed: Number(seed),
  clauses: Number(clauses),
});
const sha256 = createHash("sha256").update(JSON.stringify(cases)).digest("hex");
for (const query of cases) query.id += `:h${sha256.slice(0, 12)}`;
for (const query of cases)
  query.requests = Object.fromEntries(
    query.modes.map((mode) => [mode, request(query, mode)]),
  );
writeFileSync(
  output,
  JSON.stringify(
    { version: 1, sha256, calibration: catalog.metadata, cases },
    null,
    2,
  ) + "\n",
);
console.log(
  `Generated ${cases.length} predicates and ${cases.reduce((n, query) => n + query.modes.length, 0)} requests`,
);
