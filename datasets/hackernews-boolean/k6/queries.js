export const MODES = ["count", "bm25", "time"];
export const SHAPES = [
  "and",
  "or",
  "and_of_or",
  "or_of_and",
  "gate",
  "exclude",
];
export const MIXES = ["text_heavy", "balanced", "filters_only"];
export const PROFILES = ["broad", "sparse", "mixed", "one_sparse"];
const fields = new Set([
  "id",
  "text",
  "title",
  "time",
  "score",
  "descendants",
  "parent",
  "type",
  "by",
  "dead",
  "deleted",
]);
const operators = new Set(["=", "<", "<=", ">", ">=", "|||"]);

export function shuffled(values, seed) {
  const result = [...values];
  let state = seed >>> 0;
  for (let i = result.length - 1; i > 0; i--) {
    state = (Math.imul(state, 1664525) + 1013904223) >>> 0;
    const j = state % (i + 1);
    [result[i], result[j]] = [result[j], result[i]];
  }
  return result;
}

export function leaves(node) {
  return node.op === "leaf" ? [node.leaf] : node.children.flatMap(leaves);
}

export function tree(shape, items) {
  const nodes = items.map((leaf) => ({ op: "leaf", leaf }));
  const group = (op, children) => ({ op, children });
  const groups = (op) => {
    const result = [];
    for (let i = 0; i < nodes.length; i += 3)
      result.push(group(op, nodes.slice(i, i + 3)));
    return result;
  };
  switch (shape) {
    case "and":
      return group("and", nodes);
    case "or":
      return group("or", nodes);
    case "and_of_or":
      return group("and", groups("or"));
    case "or_of_and":
      return group("or", groups("and"));
    case "gate": {
      const gate = [...nodes].sort(
        (a, b) => a.leaf.match_fraction - b.leaf.match_fraction,
      )[0];
      return group("and", [
        gate,
        group(
          "or",
          nodes.filter((node) => node !== gate),
        ),
      ]);
    }
    case "exclude":
      return group("and", [
        group("or", nodes.slice(0, -2)),
        group("not", [group("or", nodes.slice(-2))]),
      ]);
    default:
      throw new Error(`Unknown shape: ${shape}`);
  }
}

export function generateCases(catalog, { seed = 20261008, clauses = 12 } = {}) {
  if (!Number.isSafeInteger(clauses) || clauses < 4 || clauses > 48)
    throw new Error("clauses must be between 4 and 48");
  const output = [];
  for (const mix of MIXES) {
    for (const profile of PROFILES) {
      const family =
        MIXES.indexOf(mix) * PROFILES.length + PROFILES.indexOf(profile);
      for (const shape of SHAPES) {
        const textCount =
          mix === "filters_only"
            ? 0
            : Math.round(clauses * (mix === "balanced" ? 0.5 : 2 / 3));
        const chosen = [];
        for (let i = 0; i < clauses; i++) {
          const kind = i < textCount ? "text" : "filter";
          const sparse =
            profile === "sparse" ||
            (profile === "mixed" && i % 2 === 0) ||
            (profile === "one_sparse" && i === 0);
          const available = shuffled(
            catalog.leaves.filter(
              (leaf) =>
                leaf.kind === kind &&
                leaf.match_fraction > 0 &&
                leaf.match_fraction < 1 &&
                (leaf.kind !== "text" || leaf.operator === "|||") &&
                !chosen.some((other) => other.id === leaf.id),
            ),
            seed + family * 97 + i,
          );
          available.sort((a, b) =>
            sparse
              ? a.match_fraction - b.match_fraction
              : b.match_fraction - a.match_fraction,
          );
          if (!available.length)
            throw new Error(
              `Not enough distinct ${kind} leaves for ${clauses} clauses`,
            );
          // Reuse the same leaves across shapes to isolate Boolean-tree effects.
          chosen.push(available[(family + i) % Math.min(4, available.length)]);
        }
        const items = shuffled(chosen, seed + family);
        const predicate = tree(shape, items);
        output.push({
          id: `${mix}:${profile}:${shape}:n${clauses}:s${seed}`,
          shape,
          mix,
          profile,
          clauses,
          seed,
          predicate,
          leaf_match_fractions: items.map(({ id, match_fraction }) => ({
            id,
            match_fraction,
          })),
          modes: mix === "filters_only" ? ["count", "time"] : MODES,
        });
      }
    }
  }
  return output;
}

function compile(node, params) {
  if (node.op === "leaf") {
    const { field, operator, value } = node.leaf;
    if (!fields.has(field) || !operators.has(operator))
      throw new Error("Unsupported predicate");
    params.push(value);
    const cast = field === "time" ? "::timestamptz" : "";
    return `("${field}" ${operator} $${params.length}${cast})`;
  }
  if (node.op === "not" && node.children.length === 1)
    return `(NOT ${compile(node.children[0], params)})`;
  if (!["and", "or"].includes(node.op) || !node.children.length)
    throw new Error("Invalid Boolean tree");
  return `(${node.children.map((child) => compile(child, params)).join(` ${node.op.toUpperCase()} `)})`;
}

export function request(query, mode, topK = 10) {
  if (!MODES.includes(mode) || !query.modes.includes(mode))
    throw new Error(`Unsupported mode: ${mode}`);
  if (!Number.isSafeInteger(topK) || topK < 1)
    throw new Error("TOP_K must be a positive integer");
  const params = [];
  const where = compile(query.predicate, params);
  const projection =
    mode === "count"
      ? "count(*)"
      : mode === "bm25"
        ? "id, pdb.score(id) AS relevance"
        : 'id, "time"';
  const order =
    mode === "count"
      ? ""
      : ` ORDER BY ${mode === "bm25" ? "relevance DESC" : '"time" DESC NULLS LAST'} LIMIT ${topK}`;
  return [
    `SELECT ${projection} FROM hn_items WHERE id @@@ pdb.all() AND ${where}${order}`,
    ...params,
  ];
}

export function selectCases(manifest, mode, filters = {}) {
  if (!MODES.includes(mode))
    throw new Error("MODE must be count, bm25, or time");
  const cases = manifest.cases.filter(
    (query) =>
      query.modes.includes(mode) &&
      Object.entries(filters).every(
        ([key, value]) => !value || query[key] === value,
      ),
  );
  if (!cases.length)
    throw new Error("No queries match the requested mode and filters");
  return cases;
}
