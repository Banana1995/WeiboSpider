import json
import os
import sqlite3
import time
from datetime import datetime


class TweetDB:
    def __init__(self, db_path=None):
        if db_path is None:
            db_path = os.path.join(os.getcwd(), 'data.db')
        self.conn = sqlite3.connect(db_path, check_same_thread=False)
        self.conn.execute("PRAGMA foreign_keys = ON")
        self.conn.row_factory = sqlite3.Row
        self.conn.execute("PRAGMA journal_mode=WAL")
        self._create_tables()

    def _create_tables(self):
        self.conn.executescript("""
        CREATE TABLE IF NOT EXISTS tweets (
            id              TEXT PRIMARY KEY,
            mblogid         TEXT,
            content         TEXT NOT NULL,
            user_id         TEXT NOT NULL,
            created_at      TEXT,
            reposts_count   INTEGER DEFAULT 0,
            comments_count  INTEGER DEFAULT 0,
            attitudes_count INTEGER DEFAULT 0,
            pic_urls        TEXT,
            pic_num         INTEGER DEFAULT 0,
            source          TEXT,
            ip_location     TEXT,
            is_retweet      INTEGER DEFAULT 0,
            retweet_id      TEXT,
            deleted         INTEGER DEFAULT 0,
            deleted_at      TEXT,
            url             TEXT,
            crawl_time      INTEGER
        );
        CREATE TABLE IF NOT EXISTS comments (
            id              TEXT PRIMARY KEY,
            tweet_id        TEXT NOT NULL,
            content         TEXT NOT NULL,
            created_at      TEXT,
            like_counts     INTEGER DEFAULT 0,
            ip_location     TEXT,
            comment_user    TEXT,
            reply_comment   TEXT,
            crawl_time      INTEGER,
            FOREIGN KEY (tweet_id) REFERENCES tweets(id)
        );
        CREATE INDEX IF NOT EXISTS idx_tweets_user_id ON tweets(user_id);
        CREATE INDEX IF NOT EXISTS idx_tweets_created_at ON tweets(created_at);
        CREATE INDEX IF NOT EXISTS idx_tweets_deleted ON tweets(deleted);
        CREATE INDEX IF NOT EXISTS idx_comments_tweet_id ON comments(tweet_id);
        """)
        self.conn.commit()

    def close(self):
        self.conn.close()

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.close()

    def insert_tweet(self, item):
        self.conn.execute("""
        INSERT OR IGNORE INTO tweets
            (id, mblogid, content, user_id, created_at, reposts_count,
             comments_count, attitudes_count, pic_urls, pic_num,
             source, ip_location, is_retweet, retweet_id, deleted,
             deleted_at, url, crawl_time)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, ?, ?)
        """, (
            item['_id'], item.get('mblogid'),
            item['content'], item.get('user_id', ''),
            item.get('created_at'),
            item.get('reposts_count', 0), item.get('comments_count', 0),
            item.get('attitudes_count', 0),
            json.dumps(item.get('pic_urls', [])) if isinstance(item.get('pic_urls'), list) else (item.get('pic_urls') or '[]'),
            item.get('pic_num', 0), item.get('source', ''),
            item.get('ip_location', ''), int(item.get('is_retweet', False)),
            item.get('retweet_id'), item.get('url', ''),
            item.get('crawl_time', int(time.time())),
        ))
        self.conn.commit()

    def insert_comment(self, item):
        self.conn.execute("""
        INSERT OR IGNORE INTO comments
            (id, tweet_id, content, created_at, like_counts,
             ip_location, comment_user, reply_comment, crawl_time)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        """, (
            item['_id'], item['tweet_id'], item['content'],
            item.get('created_at'), item.get('like_counts', 0),
            item.get('ip_location', ''),
            json.dumps(item.get('comment_user', {})) if isinstance(item.get('comment_user'), dict) else (item.get('comment_user') or '{}'),
            json.dumps(item.get('reply_comment')) if isinstance(item.get('reply_comment'), dict) else (item.get('reply_comment')),
            item.get('crawl_time', int(time.time())),
        ))
        self.conn.commit()

    def get_tweets(self, page=1, per_page=20, sort='desc', deleted='exclude'):
        offset = (page - 1) * per_page
        order = 'DESC' if sort == 'desc' else 'ASC'

        where = ''
        if deleted == 'exclude':
            where = 'WHERE deleted = 0'
        elif deleted == 'only':
            where = 'WHERE deleted = 1'

        sql = f"SELECT * FROM tweets {where} ORDER BY created_at {order} LIMIT ? OFFSET ?"
        rows = self.conn.execute(sql, [per_page, offset]).fetchall()
        results = []
        for row in rows:
            d = dict(row)
            d['pic_urls'] = json.loads(d.get('pic_urls', '[]') or '[]')
            d['is_retweet'] = bool(d.get('is_retweet'))
            d['deleted'] = bool(d.get('deleted'))
            results.append(d)
        return results

    def get_tweet(self, tweet_id):
        row = self.conn.execute("SELECT * FROM tweets WHERE id=?", (tweet_id,)).fetchone()
        if row is None:
            return None
        d = dict(row)
        d['pic_urls'] = json.loads(d.get('pic_urls', '[]') or '[]')
        d['is_retweet'] = bool(d.get('is_retweet'))
        d['deleted'] = bool(d.get('deleted'))
        return d

    def get_comments(self, tweet_id):
        rows = self.conn.execute(
            "SELECT * FROM comments WHERE tweet_id=? ORDER BY created_at",
            (tweet_id,)
        ).fetchall()
        results = []
        for row in rows:
            d = dict(row)
            d['comment_user'] = json.loads(d.get('comment_user', '{}') or '{}')
            if d.get('reply_comment'):
                d['reply_comment'] = json.loads(d['reply_comment'])
            results.append(d)
        return results

    def batch_delete(self, ids):
        if not ids:
            return 0
        placeholders = ','.join(['?'] * len(ids))
        now = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
        cur = self.conn.execute(
            f"UPDATE tweets SET deleted=1, deleted_at=? WHERE id IN ({placeholders})",
            [now] + list(ids)
        )
        self.conn.commit()
        return cur.rowcount

    def restore_tweets(self, ids):
        if not ids:
            return 0
        placeholders = ','.join(['?'] * len(ids))
        cur = self.conn.execute(
            f"UPDATE tweets SET deleted=0, deleted_at=NULL WHERE id IN ({placeholders})",
            list(ids)
        )
        self.conn.commit()
        return cur.rowcount

    def get_tweet_ids(self):
        rows = self.conn.execute("SELECT mblogid FROM tweets WHERE deleted=0").fetchall()
        return [r[0] for r in rows]

    def stats(self):
        total = self.conn.execute("SELECT COUNT(*) FROM tweets").fetchone()[0]
        deleted_count = self.conn.execute(
            "SELECT COUNT(*) FROM tweets WHERE deleted=1"
        ).fetchone()[0]
        comments_count = self.conn.execute(
            "SELECT COUNT(*) FROM comments"
        ).fetchone()[0]
        return {
            'total_tweets': total,
            'deleted_tweets': deleted_count,
            'total_comments': comments_count,
        }
