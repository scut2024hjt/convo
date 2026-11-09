"""Tests of fixture rendering only; NOT database integration tests."""
import unittest
from pathlib import Path
from prepare_isolated_sql import render


class TestRender(unittest.TestCase):
    def setUp(self):
        self.sql = Path(__file__).resolve().parents[2].joinpath("init.sql").read_text()

    def test_safe_database_and_migration(self):
        result = render(self.sql, "convo_t02")
        self.assertIn("CREATE DATABASE IF NOT EXISTS `convo_t02`;", result)
        self.assertIn("USE `convo_t02`;", result)
        self.assertIn("CREATE TABLE follow_delivery_task", result)
        self.assertIn("DROP CHECK chk_projection_kind", result)
        self.assertEqual(result.count("CREATE DATABASE IF NOT EXISTS `convo_t02`;"), 1)

    def test_reject_unsafe_target(self):
        for name in ["convo", "production", "convo_t01", "foo;DROP DATABASE convo", "../convo_t02"]:
            with self.subTest(name=name), self.assertRaises(ValueError):
                render(self.sql, name)


if __name__ == "__main__":
    unittest.main()
