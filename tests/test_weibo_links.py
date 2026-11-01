import io
import os
import sys
from email.message import Message
from unittest.mock import Mock
from urllib.error import HTTPError, URLError

import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'weibospider'))

import weibo_links


SHORT = 'https://t.cn/Test1234'
PHOTO = 'https://photo.weibo.com/h5/comment/compic_id/1022:230597example'
IMAGE = 'https://wx3.sinaimg.cn/bmiddle/example.jpg'


def redirect(url, location):
    headers = Message()
    headers['Location'] = location
    return HTTPError(url, 302, 'Found', headers, io.BytesIO())


def page(html):
    response = io.BytesIO(html.encode())
    response.headers = Message()
    response.headers['Content-Type'] = 'text/html; charset=utf-8'
    return response


@pytest.fixture
def opener(monkeypatch):
    weibo_links._cache.clear()
    fake = Mock()
    monkeypatch.setattr(weibo_links, 'build_opener', lambda *args: fake)
    return fake


def test_comment_photo_is_resolved_and_shared_between_http_and_https(opener):
    opener.open.side_effect = [redirect(SHORT, PHOTO), page(f'<img src="{IMAGE}">')]
    assert weibo_links.resolve_image_link(SHORT.replace('https:', 'http:')) == IMAGE
    assert weibo_links.resolve_image_link(SHORT) == IMAGE
    assert [call.args[0].full_url for call in opener.open.call_args_list] == [SHORT, PHOTO]


@pytest.mark.parametrize('target', [
    'https://example.org/article',
    'http://127.0.0.1/private',
    'http://169.254.169.254/latest/meta-data/',
    'file:///etc/passwd',
    'https://photo.weibo.com.evil.test/h5/comment/compic_id/1',
    'https://photo.weibo.com@127.0.0.1/h5/comment/compic_id/1',
    'https://photo.weibo.com:8443/h5/comment/compic_id/1',
    'https://photo.weibo.com/other-path',
])
def test_non_image_and_untrusted_redirects_are_never_fetched(opener, target):
    opener.open.side_effect = redirect(SHORT, target)
    assert weibo_links.resolve_image_link(SHORT) is None
    assert opener.open.call_count == 1


def test_unsafe_image_source_is_ignored_and_protocol_relative_image_is_supported(opener):
    opener.open.side_effect = [redirect(SHORT, PHOTO), page(
        '<img src="http://127.0.0.1/image.jpg">'
        '<img src="https://wx3.sinaimg.cn.evil.test/image.jpg">'
        '<img src="//WX3.SINAIMG.CN/bmiddle/example.jpg">')]
    assert weibo_links.resolve_image_link(SHORT) == IMAGE


def test_direct_image_redirect_does_not_download_the_image(opener):
    opener.open.side_effect = redirect(SHORT, IMAGE)
    assert weibo_links.resolve_image_link(SHORT) == IMAGE
    assert opener.open.call_count == 1


def test_transient_failure_can_be_retried(opener):
    opener.open.side_effect = [URLError('timeout'), redirect(SHORT, IMAGE)]
    with pytest.raises(OSError):
        weibo_links.resolve_image_link(SHORT)
    assert weibo_links.resolve_image_link(SHORT) == IMAGE


def test_redirect_loop_is_bounded(opener):
    def bounce(*args, **kwargs):
        raise redirect(SHORT, SHORT)
    opener.open.side_effect = bounce
    with pytest.raises(OSError, match='too many'):
        weibo_links.resolve_image_link(SHORT)
    assert opener.open.call_count == 4


def test_oversized_photo_page_is_rejected(opener):
    opener.open.side_effect = [redirect(SHORT, PHOTO), page('x' * (64 * 1024 + 1))]
    with pytest.raises(OSError, match='too large'):
        weibo_links.resolve_image_link(SHORT)


@pytest.mark.parametrize('url', ['', 'https://example.org/image', 'http://t.cn@localhost/a',
                                 'https://t.cn/a?next=localhost', 'https://t.cn/a\n'])
def test_endpoint_rejects_invalid_urls_without_network_access(opener, url):
    import app
    response = app.app.test_client().get('/api/weibo/image-link', query_string={'url': url})
    assert response.status_code == 400
    opener.open.assert_not_called()


def test_endpoint_returns_image_and_non_image_results(opener):
    import app
    client = app.app.test_client()
    opener.open.side_effect = [redirect(SHORT, IMAGE), redirect(SHORT, 'https://example.org/')]
    response = client.get('/api/weibo/image-link', query_string={'url': SHORT})
    assert response.status_code == 200
    assert response.json == {'image_url': IMAGE}
    response = client.get('/api/weibo/image-link', query_string={'url': 'https://t.cn/Other123'})
    assert response.status_code == 200
    assert response.json == {'image_url': None}


def test_endpoint_network_failure_does_not_expose_upstream_details(opener):
    import app
    opener.open.side_effect = URLError('upstream details')
    response = app.app.test_client().get('/api/weibo/image-link', query_string={'url': SHORT})
    assert response.status_code == 502
    assert 'upstream details' not in response.get_data(as_text=True)
