#!/usr/bin/env python3
"""Apply a previously reviewed, version- and payload-pinned removal plan.

No discovery or duplicate guessing. By default exercise the exact transaction
and roll it back. --apply requires a new SQLite backup and all postconditions.
Keep plans and backups private, outside the tracked checkout.
"""

import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import sys


def check(condition, message):
    if not condition:
        raise ValueError(message)


def integrity(conn):
    check(conn.execute("PRAGMA integrity_check").fetchall() == [("ok",)], "integrity check failed")
    check(not conn.execute("PRAGMA foreign_key_check").fetchall(), "foreign key check failed")


def backup(conn, path):
    # Exclusive creation: never overwrite a recovery point.
    fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    os.close(fd)
    dst = sqlite3.connect(path)
    try:
        conn.backup(dst)
        integrity(dst)
    finally:
        dst.close()


def records(db, account):
    return dict(db.execute("SELECT id,payload FROM account_records WHERE account_id=?", (account,)))


def totals(db, account):
    return list(db.execute("""SELECT count(*),coalesce(sum(max(flow_minor,0)),0),
        coalesce(sum(max(-flow_minor,0)),0) FROM account_records WHERE account_id=? AND NOT voided""", (account,)).fetchone())


def repair(db, plan):
    aid = plan["account_id"]
    check(db.execute("SELECT name,currency FROM accounts WHERE id=?", (aid,)).fetchone() ==
          (plan["account_name"], plan["currency"]), "account mismatch")
    revision, = db.execute("SELECT max(id) FROM audit_log WHERE account_id=? AND entity_type='account_record'", (aid,)).fetchone()
    check(revision == plan["expected_revision"], "account records changed since review")
    pairs = plan["pairs"]
    check(0 < len(pairs) <= 100, "invalid pair count")
    ids = [pair[role]["id"] for pair in pairs for role in ("remove", "keep")]
    check(len(set(ids)) == len(ids), "overlapping targets")
    before = records(db, aid)
    check(totals(db, aid) == plan["before"], "opening totals mismatch")
    stamp = datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z")
    for pair in pairs:
        for role in ("remove", "keep"):
            ref = pair[role]
            raw = before[ref["id"]]
            row = json.loads(raw)
            check(row["account_id"] == aid and row["id"] == ref["id"] and row["origin"] == "import"
                  and row["kind"] == "cash_flow" and not row["voided"] and row["version"] == str(ref["version"])
                  and hashlib.sha256(raw.encode()).hexdigest() == ref["sha256"], "target no longer matches reviewed evidence")
        old, keep = pair["remove"], pair["keep"]
        meta = {"from_date": json.loads(before[old["id"]])["date"], "reason": pair["reason"],
                "kept_id": keep["id"], "kept_version": keep["version"]}
        db.execute("""INSERT INTO audit_log(correlation_id,action,entity_type,entity_id,account_id,version,
            recorded_at,source,before_json,after_json,metadata_json) VALUES(?, 'remove_duplicate',
            'account_record',?,?,?,?, 'human',?,'null',?)""", (plan["operation_id"], old["id"], aid, old["version"] + 1,
            stamp, before.pop(old["id"]), json.dumps(meta, ensure_ascii=False)))
    # Compare every remaining record (also voided ones), not just the row count.
    check(records(db, aid) == before, "postcondition failed: unexpected records changed")
    check(totals(db, aid) == plan["after"], "closing totals mismatch")
    check(not db.execute("PRAGMA foreign_key_check").fetchall(), "foreign key check failed")


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--db", required=True, type=Path)
    p.add_argument("--plan", required=True, help="reviewed JSON file, or - for stdin")
    p.add_argument("--apply", action="store_true")
    p.add_argument("--backup", type=Path)
    args = p.parse_args()
    if args.apply and not args.backup:
        p.error("--apply requires --backup NEW_PATH")
    os.umask(0o077)
    plan = json.loads(sys.stdin.read() if args.plan == "-" else Path(args.plan).read_text())
    db = sqlite3.connect(args.db.resolve().as_uri() + "?mode=rw", uri=True, isolation_level=None, timeout=5)
    try:
        db.execute("PRAGMA foreign_keys=ON")
        # Acquire the writer reservation first. The backup connection then sees
        # exactly the pre-transaction state, even on a running WAL database.
        db.execute("BEGIN IMMEDIATE")
        if args.backup:
            source = sqlite3.connect(args.db.resolve().as_uri() + "?mode=ro", uri=True)
            try:
                backup(source, args.backup)
            finally:
                source.close()
        repair(db, plan)
        if args.apply:
            db.commit()
        else:
            db.rollback()
        print(json.dumps({"committed": args.apply, "removed": len(plan["pairs"]), "verified_totals_minor": plan["after"]}))
    finally:
        db.close()  # rolls back every partial removal on exception


if __name__ == "__main__":
    main()
