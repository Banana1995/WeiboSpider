"""Global search over tweets, comments and annotations.

Two query paths (see docs/superpowers/specs/2026-08-29-global-search-design.md):
  - keyword length >= 3  -> FTS5 MATCH on search_index (uses trigram index)
  - keyword length  < 3  -> LIKE over source tables (faster than LIKE on FTS5)
"""

# FTS5 MATCH needs >= 3 chars with the trigram tokenizer; shorter keywords
# return zero rows, so they take the LIKE path instead.
MIN_MATCH_LEN = 3


def fts5_quote(s):
    """Quote a user keyword as an FTS5 phrase literal.

    Raw user input is NOT a safe FTS5 query: characters like ' " * : -
    and bare AND/OR/NOT raise OperationalError. Wrapping in double quotes
    turns the whole thing into a phrase, and inner quotes are doubled.
    """
    return '"' + (s or '').replace('"', '""') + '"'


def build_search_sql(q, page=1, per_page=20, source_type='all',
                     start_date=None, end_date=None):
    """Build (sql, params) for a search query.

    Returns rows shaped: doc_id, source_type, tweet_id, highlight,
                         id, content, created_at, user_id, screen_name, platform
    `highlight` is filled by snippet() on the MATCH path and is NULL on the
    LIKE path (the caller highlights in Python via make_highlight()).
    """
    page = max(int(page or 1), 1)
    per_page = min(max(int(per_page or 20), 1), 100)
    offset = (page - 1) * per_page

    filters = []
    filter_params = []
    if source_type and source_type != 'all':
        filters.append("s.source_type = ?")
        filter_params.append(source_type)
    if start_date:
        filters.append("t.created_at >= ?")
        filter_params.append(start_date)
    if end_date:
        filters.append("t.created_at <= ?")
        filter_params.append(end_date + ' 23:59:59')
    extra = (' AND ' + ' AND '.join(filters)) if filters else ''

    if len(q) >= MIN_MATCH_LEN:
        # Path A: FTS5 MATCH. Left operand MUST be the table name.
        # snippet() column index 3 == the `text` column.
        sql = f"""
        SELECT s.doc_id, s.source_type, s.tweet_id,
               snippet(search_index, 3, '<mark>', '</mark>', '…', 12) AS highlight,
               t.id, t.content, t.created_at, t.user_id, t.screen_name, t.platform
          FROM search_index s
          JOIN tweets t ON t.id = s.tweet_id
         WHERE search_index MATCH ?
           AND t.deleted = 0
           {extra}
         ORDER BY t.created_at DESC
         LIMIT ? OFFSET ?
        """
        params = [fts5_quote(q)] + filter_params + [per_page, offset]
        return sql, params

    # Path B: short keyword -> LIKE over source tables (never on the FTS5 table).
    like = f'%{q}%'
    sql = f"""
        SELECT s.doc_id, s.source_type, s.tweet_id,
               NULL AS highlight,
               t.id, t.content, t.created_at, t.user_id, t.screen_name, t.platform
          FROM (
                SELECT id AS doc_id, 'tweet' AS source_type, id AS tweet_id
                  FROM tweets
                 WHERE content LIKE ? OR retweet_content LIKE ?
                UNION ALL
                SELECT id, 'comment', tweet_id
                  FROM comments
                 WHERE content LIKE ?
                UNION ALL
                SELECT id, 'annotation', tweet_id
                  FROM annotations
                 WHERE comment LIKE ? OR selected_text LIKE ?
               ) s
          JOIN tweets t ON t.id = s.tweet_id
         WHERE t.deleted = 0
           {extra}
         ORDER BY t.created_at DESC
         LIMIT ? OFFSET ?
    """
    params = [like] * 5 + filter_params + [per_page, offset]
    return sql, params
