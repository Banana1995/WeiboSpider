"""Global search over tweets, comments and annotations.

Two query paths (see docs/superpowers/specs/2026-08-29-global-search-design.md):
  - keyword length >= 3  -> FTS5 MATCH on search_index (uses trigram index)
  - keyword length  < 3  -> LIKE over source tables (faster than LIKE on FTS5)
"""

import html

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
    """Build (sql, params) for a search query, aggregated per tweet.

    Returns one row per distinct tweet (the full tweets row via t.*), plus:
      - hit_count: number of matching docs for that tweet
      - hits:      JSON array of {"doc_id", "source_type"} via json_group_array

    The full matched text is NOT returned here (search_index.text concatenates
    content+retweet / comment+selected_text, losing field boundaries). The
    caller backfills text from the source tables by doc_id and highlights in
    Python with highlight_all() -- snippet() cannot run in an aggregate
    context, so it is gone entirely.
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
        sql = f"""
        SELECT COUNT(*) AS hit_count,
               json_group_array(json_object(
                   'doc_id', s.doc_id,
                   'source_type', s.source_type
               )) AS hits,
               t.*
          FROM search_index s
          JOIN tweets t ON t.id = s.tweet_id
         WHERE search_index MATCH ?
           AND t.deleted = 0
           {extra}
         GROUP BY t.id
         ORDER BY t.created_at DESC
         LIMIT ? OFFSET ?
        """
        params = [fts5_quote(q)] + filter_params + [per_page, offset]
        return sql, params

    # Path B: short keyword -> LIKE over source tables (never on the FTS5 table).
    esc = q.replace('\\', '\\\\').replace('%', '\\%').replace('_', '\\_')
    like = f'%{esc}%'
    sql = f"""
        SELECT COUNT(*) AS hit_count,
               json_group_array(json_object(
                   'doc_id', s.doc_id,
                   'source_type', s.source_type
               )) AS hits,
               t.*
          FROM (
                SELECT id AS doc_id, 'tweet' AS source_type, id AS tweet_id
                  FROM tweets
                 WHERE content LIKE ? ESCAPE '\\' OR retweet_content LIKE ? ESCAPE '\\'
                UNION ALL
                SELECT id, 'comment', tweet_id
                  FROM comments
                 WHERE content LIKE ? ESCAPE '\\'
                UNION ALL
                SELECT id, 'annotation', tweet_id
                  FROM annotations
                 WHERE comment LIKE ? ESCAPE '\\' OR selected_text LIKE ? ESCAPE '\\'
               ) s
          JOIN tweets t ON t.id = s.tweet_id
         WHERE t.deleted = 0
           {extra}
         GROUP BY t.id
         ORDER BY t.created_at DESC
         LIMIT ? OFFSET ?
    """
    params = [like] * 5 + filter_params + [per_page, offset]
    return sql, params


def highlight_all(text, q):
    """Return `text` fully HTML-escaped with every occurrence of `q` in <mark>.

    Unlike make_highlight(), this does NOT truncate: the whole text comes
    back (escaped) with all matches wrapped. Escaping happens first, then
    <mark> is inserted around each match, so markup in the source text can
    never inject HTML.
    """
    text = text or ''
    if not text:
        return ''
    escaped = html.escape(text)
    if not q:
        return escaped
    ql = html.escape(q).lower()
    out = []
    i = 0
    lower = escaped.lower()
    while True:
        idx = lower.find(ql, i)
        if idx < 0:
            out.append(escaped[i:])
            break
        out.append(escaped[i:idx])
        out.append('<mark>')
        out.append(escaped[idx:idx + len(ql)])
        out.append('</mark>')
        i = idx + len(ql)
    return ''.join(out)
