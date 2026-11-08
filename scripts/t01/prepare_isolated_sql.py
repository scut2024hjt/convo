#!/usr/bin/env python3
"""Render init.sql for one disposable T01 database; never writes to MySQL."""
import argparse
import re
import sys
from pathlib import Path


def render(source: str, database: str) -> str:
    if not re.fullmatch(r"[a-z][a-z0-9_]*_t01", database):
        raise ValueError("database name must end with _t01 and use safe lowercase characters")
    create = "CREATE DATABASE IF NOT EXISTS convo;"
    use = "USE convo;"
    if source.count(create) != 1 or source.count(use) != 1:
        raise ValueError("unexpected init.sql database header; refusing to transform")
    return source.replace(create, f"CREATE DATABASE IF NOT EXISTS `{database}`;").replace(
        use, f"USE `{database}`;"
    )


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--database", default="convo_t01")
    args = parser.parse_args()
    try:
        sys.stdout.write(render(Path(__file__).resolve().parents[2].joinpath("init.sql").read_text(), args.database))
    except ValueError as error:
        parser.error(str(error))
