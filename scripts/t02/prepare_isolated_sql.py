#!/usr/bin/env python3
"""Render T02 init.sql for a uniquely named disposable database; no DB connection."""
import argparse
import re
import sys
from pathlib import Path


def render(source: str, database: str) -> str:
    if not re.fullmatch(r"[a-z][a-z0-9_]*_t02", database):
        raise ValueError("test database must be lowercase and end in _t02")
    create = "CREATE DATABASE IF NOT EXISTS convo;"
    use = "USE convo;"
    if source.count(create) != 1 or source.count(use) != 1:
        raise ValueError("unexpected init.sql header; refusing to transform")
    if "follow_delivery_task" not in source or "chk_projection_kind" not in source:
        raise ValueError("T02 migration is missing from init.sql")
    return source.replace(create, f"CREATE DATABASE IF NOT EXISTS `{database}`;").replace(
        use, f"USE `{database}`;"
    )


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--database", default="convo_t02")
    args = parser.parse_args()
    try:
        sys.stdout.write(render(Path(__file__).resolve().parents[2].joinpath("init.sql").read_text(), args.database))
    except ValueError as e:
        parser.error(str(e))
