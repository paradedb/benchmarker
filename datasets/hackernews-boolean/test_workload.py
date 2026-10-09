import unittest

from calibrate import filter_leaves
from pg import bind, literal


class WorkloadTests(unittest.TestCase):
    def test_bind_does_not_confuse_parameter_numbers_or_replace_inside_values(self):
        self.assertEqual(
            bind(["SELECT $1, $10, $2", "$2'", *range(2, 11)]), "SELECT '$2''', 10, 2"
        )
        self.assertEqual(literal(False), "FALSE")
        self.assertEqual(literal(None), "NULL")

    def test_filter_rates_include_nulls_in_denominator_and_deduplicate_ties(self):
        rows = [
            dict(
                id=i,
                time=f"2020-{i + 1:02d}-01",
                score=i if i else None,
                descendants=i,
                parent=i,
                type="story" if i % 2 else "comment",
                by=str(i),
                dead=False,
                deleted=i == 0,
            )
            for i in range(10)
        ]
        predicates = filter_leaves(rows)
        self.assertEqual(len(predicates), len({leaf["id"] for leaf in predicates}))
        self.assertFalse(any(leaf["field"] == "dead" for leaf in predicates))
        leaf = next(
            leaf
            for leaf in predicates
            if leaf["field"] == "score"
            and leaf["operator"] == ">="
            and leaf["value"] == 1
        )
        self.assertEqual(leaf["match_fraction"], 0.9)


if __name__ == "__main__":
    unittest.main()
