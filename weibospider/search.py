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
