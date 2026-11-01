"""Resolve archived t.cn image links without changing the stored tweet text."""

import re
import threading
import time
from collections import OrderedDict
from html.parser import HTMLParser
from urllib.error import HTTPError
from urllib.parse import urljoin, urlsplit, urlunsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener


_SHORT_URL = re.compile(r'https?://t\.cn/[A-Za-z0-9]{1,32}')
_IMAGE_HOSTS = {'wx1.sinaimg.cn', 'wx2.sinaimg.cn',
                'wx3.sinaimg.cn', 'wx4.sinaimg.cn'}
_MAX_PAGE_BYTES = 64 * 1024
_cache = OrderedDict()
_cache_lock = threading.Lock()


def _safe_url(url):
    """Only follow Weibo's shortener, comment-photo pages and proxied images."""
    if not isinstance(url, str) or re.search(r'[\s\\\x00-\x1f\x7f]', url):
        return None
    try:
        parts = urlsplit(url)
        if (parts.scheme not in ('http', 'https') or parts.username is not None
                or parts.password is not None or parts.port is not None):
            return None
    except ValueError:
        return None
    if parts.hostname in _IMAGE_HOSTS:
        pass
    elif parts.hostname == 'photo.weibo.com' and parts.path.startswith('/h5/comment/compic_id/'):
        pass
    elif not _SHORT_URL.fullmatch(url):
        return None
    return urlunsplit(('https', parts.hostname, parts.path, parts.query, ''))


class _NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # Validate each Location before making the next request (never follow
        # arbitrary article links or redirects to local/private addresses).
        return None


class _PhotoParser(HTMLParser):
    def __init__(self, page_url):
        super().__init__()
        self.page_url = page_url
        self.image_url = None

    def handle_starttag(self, tag, attrs):
        if tag != 'img' or self.image_url:
            return
        src = dict(attrs).get('src')
        url = _safe_url(urljoin(self.page_url, src)) if src else None
        if url and urlsplit(url).hostname in _IMAGE_HOSTS:
            self.image_url = url


def _resolve_image(url):
    opener = build_opener(_NoRedirect())
    deadline = time.monotonic() + 8
    for _ in range(4):
        url = _safe_url(url)
        if not url:
            return None
        if urlsplit(url).hostname in _IMAGE_HOSTS:
            return url
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise TimeoutError('image link lookup timed out')
        req = Request(url, headers={'User-Agent': 'Mozilla/5.0',
                                    'Referer': 'https://weibo.com/'})
        try:
            response = opener.open(req, timeout=remaining)
        except HTTPError as exc:
            if exc.code not in (301, 302, 303, 307, 308):
                exc.close()
                raise
            location = exc.headers.get('Location')
            exc.close()
            if not location:
                return None
            url = urljoin(url, location)
            continue
        with response:
            if urlsplit(url).hostname != 'photo.weibo.com':
                return None
            if response.headers.get_content_type() != 'text/html':
                return None
            body = response.read(_MAX_PAGE_BYTES + 1)
            if len(body) > _MAX_PAGE_BYTES:
                raise OSError('image page too large')
            parser = _PhotoParser(url)
            parser.feed(body.decode('utf-8', errors='replace'))
            return parser.image_url
    raise OSError('too many image link redirects')


def resolve_image_link(url):
    if not isinstance(url, str) or not _SHORT_URL.fullmatch(url):
        raise ValueError('invalid Weibo short link')
    url = url.replace('http://', 'https://', 1)
    with _cache_lock:
        cached = _cache.get(url)
        if cached and cached[0] > time.monotonic():
            _cache.move_to_end(url)
            return cached[1]
    image_url = _resolve_image(url)
    with _cache_lock:
        # Retry non-image results sooner; transient network errors are not cached.
        ttl = 86400 if image_url else 3600
        _cache[url] = (time.monotonic() + ttl, image_url)
        _cache.move_to_end(url)
        while len(_cache) > 512:
            _cache.popitem(last=False)
    return image_url
