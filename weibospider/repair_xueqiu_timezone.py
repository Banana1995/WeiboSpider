"""Explicit, backed-up repair of verified legacy Xueqiu wall-clock times.

No network requests or automatic timezone inference. Inspect upstream samples
and the old deployment timezone before choosing --legacy-timezone.
"""
import argparse
import json
import os
from pathlib import Path
import sqlite3

from db import TweetDB


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--db', required=True)
    parser.add_argument('--legacy-timezone', required=True,
                        choices=('UTC', 'Asia/Shanghai'))
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--backup', help='New private SQLite backup file; required for --apply')
    args = parser.parse_args()
    source_path = Path(args.db).resolve()
    if not source_path.is_file():
        parser.error('--db must be an existing database')
    if args.apply:
        if not args.backup:
            parser.error('--apply requires --backup')
        backup = Path(args.backup).resolve()
        backup.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        # Refuse to overwrite an existing backup, and never create a publicly
        # readable snapshot (the database contains credentials).
        fd = os.open(str(backup), os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        os.close(fd)
        source = sqlite3.connect(source_path.as_uri() + '?mode=ro', uri=True)
        dest = sqlite3.connect(str(backup))
        try:
            source.backup(dest)
        finally:
            dest.close()
            source.close()
        print(json.dumps({'backup': str(backup)}))
    with TweetDB(str(source_path)) as db:
        counts = db.migrate_xueqiu_timestamps(args.legacy_timezone, apply=args.apply)
        print(json.dumps({'applied': args.apply, 'eligible_rows': counts}))


if __name__ == '__main__':
    main()
