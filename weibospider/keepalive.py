# weibospider/keepalive.py
"""Read-only cookie liveness checks and ALF expiry metadata.

Cookie renewal is handled by the local browser-cookie sync tool.
"""
import json
import logging
import re
import urllib.request

logger = logging.getLogger(__name__)

USER_AGENT = ('Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) '
              'AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36')

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
