import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from prepare_isolated_sql import render

SOURCE = Path(__file__).resolve().parents[2].joinpath("init.sql").read_text()


class TestIsolatedT03SQL(unittest.TestCase):
    def test_full_schema_and_safe_name(self):
        sql = render(SOURCE, "convo_ci_51_t03")
        self.assertIn("CREATE DATABASE IF NOT EXISTS `convo_ci_51_t03`;", sql)
        self.assertIn("USE `convo_ci_51_t03`;", sql)
        self.assertIn("CREATE TABLE like_semantics_state", sql)
        self.assertIn("VALUES(1,'ready')", sql)
        self.assertNotIn("USE convo;", sql)

    def test_reject_production_and_invalid_names(self):
        for name in ("convo", "convo_t02", "convo-test_t03", "Convo_t03", "_t03", "convo_t03;DROP"):
            with self.subTest(name=name), self.assertRaises(ValueError):
                render(SOURCE, name)

    def test_missing_schema_is_fatal(self):
        with self.assertRaises(ValueError):
            render(SOURCE.replace("CREATE TABLE like_semantics_state", "CREATE TABLE other_state"), "convo_ci_t03")


if __name__ == "__main__":
    unittest.main()
