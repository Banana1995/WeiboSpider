# weibospider/keepalive.py
"""Cookie keepalive: ping weibo.com to refresh session cookie via Set-Cookie headers.

Uses only stdlib (urllib) to avoid adding dependencies.
"""
import json
import logging
import re
import time
import urllib.request

logger = logging.getLogger(__name__)

KEEPALIVE_URL = 'https://weibo.com/'
USER_AGENT = ('Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) '
              'AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36')

# Run keepalive daily when ALF expiry is within this many seconds
KEEPALIVE_WINDOW_SECONDS = 3 * 24 * 3600  # 3 days

# Probe the mymblog API to test real session liveness (ALF alone is not enough:
# weibo can invalidate a session server-side while ALF still looks valid).
PROBE_URL = 'https://weibo.com/ajax/statuses/mymblog?uid={uid}&page=1&feature=1'


def get_alf_expiry(cookie_string):
    """Extract ALF expiry timestamp from cookie string.

    ALF format: 'ALF=<timestamp>' or 'ALF=02_<timestamp>'.
    Returns int timestamp or None if not found / unparseable.
    """
    if not cookie_string:
        return None
    m = re.search(r'ALF=(?:\d+_)?(\d+)', cookie_string)
    if not m:
        return None
    try:
        return int(m.group(1))
    except ValueError:
        return None


def should_keepalive_now(cookie_string, now=None):
    """Return True if ALF expiry is within KEEPALIVE_WINDOW_SECONDS.

    If ALF is missing or unparseable, returns True (keepalive anyway,
    better safe than sorry).
    """
    if now is None:
        now = time.time()
    expiry = get_alf_expiry(cookie_string)
    if expiry is None:
        return True
    return (expiry - now) < KEEPALIVE_WINDOW_SECONDS


def _probe_mymblog(cookie_string, uid):
    """GET the mymblog API with the given cookie, return the response object."""
    req = urllib.request.Request(PROBE_URL.format(uid=uid), headers={
        'User-Agent': USER_AGENT,
        'Cookie': cookie_string,
        'Referer': 'https://weibo.com/',
        'Accept': 'application/json, text/plain, */*',
        'X-Requested-With': 'XMLHttpRequest',
    })
    return urllib.request.urlopen(req, timeout=15)


def check_cookie_alive(cookie_string, uid='3962719063'):
    """Probe the weibo API to check whether the session is actually alive.

    Returns:
      True   - session alive (API returned ok=1)
      False  - session dead (ok=-100, login redirect, or empty cookie)
      None   - unknown (network error / unparseable response)
    """
    if not cookie_string:
        return False
    try:
        resp = _probe_mymblog(cookie_string, uid)
    except Exception as e:
        logger.warning("Cookie liveness probe request failed: %s", e)
        return None
    try:
        raw = resp.read()
        ctype = (resp.headers.get('Content-Type') or '')
        if 'json' in ctype.lower():
            data = json.loads(raw.decode('utf-8', errors='replace'))
            ok = data.get('ok', data.get('code'))
            return ok == 1
        # HTML response (login redirect / captcha) means the session is dead.
        return False
    except Exception as e:
        logger.warning("Cookie liveness probe parse failed: %s", e)
        return None


def _merge_set_cookie_into_cookie_string(old_cookie, set_cookie_headers):
    """Merge Set-Cookie response headers into existing cookie string.

    Parses each Set-Cookie header (which may contain attributes like
    Path, Domain, Expires, HttpOnly) and updates the corresponding key
    in the old cookie string. Keys not present in Set-Cookie are preserved.
    """
    cookies = {}
    for part in old_cookie.split(';'):
        part = part.strip()
        if not part or '=' not in part:
            continue
        k, _, v = part.partition('=')
        cookies[k.strip()] = v.strip()

    for header in set_cookie_headers:
        if not header:
            continue
        first = header.split(';')[0].strip()
        if '=' not in first:
            continue
        k, _, v = first.partition('=')
        k = k.strip()
        v = v.strip()
        # Servers send 'Set-Cookie: key=deleted' to expire a cookie.
        # Don't store 'deleted' as a value — remove the key instead.
        if v.lower() in ('deleted', ''):
            cookies.pop(k, None)
        else:
            cookies[k] = v

    return '; '.join(f'{k}={v}' for k, v in cookies.items())


def _ping_weibo(cookie_string):
    """GET weibo.com with current cookie, return the response object.

    Raises on network error.
    """
    req = urllib.request.Request(KEEPALIVE_URL, headers={
        'User-Agent': USER_AGENT,
        'Cookie': cookie_string,
        'Accept': 'text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8',
        'Accept-Language': 'zh-CN,zh;q=0.9,en;q=0.8',
    })
    return urllib.request.urlopen(req, timeout=15)


def refresh_cookie(db):
    """Ping weibo.com, merge any Set-Cookie response into stored cookie.

    Returns dict with:
      {'refreshed': True/False} on success
      {'error': '...'} on failure
    """
    cookie = db.get_config('cookie', '') or ''
    if not cookie:
        return {'error': 'no cookie configured'}

    try:
        resp = _ping_weibo(cookie)
    except Exception as e:
        logger.warning("Cookie keepalive request failed: %s", e)
        return {'error': str(e)}

    set_cookie_headers = resp.headers.get_all('Set-Cookie') or []
    if not set_cookie_headers:
        logger.info("Cookie keepalive: no Set-Cookie in response, cookie unchanged")
        return {'refreshed': False}

    new_cookie = _merge_set_cookie_into_cookie_string(cookie, set_cookie_headers)
    if new_cookie == cookie:
        logger.info("Cookie keepalive: Set-Cookie present but no values changed")
        return {'refreshed': False}

    db.set_config('cookie', new_cookie)
    logger.info("Cookie keepalive: cookie refreshed")
    return {'refreshed': True}
