#!/usr/bin/env python3
"""Render a disposable T03 database init script; never connect to a database."""
import argparse
import re
import sys
from pathlib import Path


def render(source: str, database: str) -> str:
    if not re.fullmatch(r"[a-z][a-z0-9_]*_t03", database):
        raise ValueError("T03 test database must have a safe lowercase *_t03 name")
    create = "CREATE DATABASE IF NOT EXISTS convo;"
    use = "USE convo;"
    if source.count(create) != 1 or source.count(use) != 1:
        raise ValueError("unexpected init.sql header; refusing to generate test SQL")
    for required in (
        "CREATE TABLE like_semantics_state",
        "INSERT INTO like_semantics_state(id,phase) VALUES(1,'ready')",
        "CONSTRAINT `chk_post_vote_direction` CHECK (`direction` IN (0, 1))",
        "CREATE TABLE follow_delivery_task",
    ):
        if required not in source:
            raise ValueError(f"required T01/T02/T03 schema is missing: {required}")
    return source.replace(create, f"CREATE DATABASE IF NOT EXISTS `{database}`;").replace(
        use, f"USE `{database}`;"
    )


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--database", required=True, help="disposable, isolated *_t03 database")
    args = parser.parse_args()
    try:
        sys.stdout.write(render(Path(__file__).resolve().parents[2].joinpath("init.sql").read_text(), args.database))
    except ValueError as exc:
        parser.error(str(exc))
