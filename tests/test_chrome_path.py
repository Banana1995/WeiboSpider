import os
import sys
import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'weibospider'))


class TestChromePath:
    def test_env_var_takes_priority(self, monkeypatch):
        from app import _get_chrome_path
        monkeypatch.setenv('CHROME_PATH', '/usr/bin/my-chrome')
        monkeypatch.setattr(os.path, 'exists', lambda p: p == '/usr/bin/my-chrome')
        assert _get_chrome_path() == '/usr/bin/my-chrome'

    def test_fallback_to_macos_path(self, monkeypatch):
        from app import _get_chrome_path
        monkeypatch.delenv('CHROME_PATH', raising=False)
        mac_path = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
        monkeypatch.setattr(os.path, 'exists', lambda p: p == mac_path)
        assert _get_chrome_path() == mac_path

    def test_fallback_to_which_chromium(self, monkeypatch):
        from app import _get_chrome_path
        monkeypatch.delenv('CHROME_PATH', raising=False)
        monkeypatch.setattr(os.path, 'exists', lambda p: False)
        import shutil
        monkeypatch.setattr(shutil, 'which', lambda name: f'/usr/bin/{name}' if name == 'chromium' else None)
        assert _get_chrome_path() == '/usr/bin/chromium'

    def test_raises_when_nothing_found(self, monkeypatch):
        from app import _get_chrome_path
        monkeypatch.delenv('CHROME_PATH', raising=False)
        monkeypatch.setattr(os.path, 'exists', lambda p: False)
        import shutil
        monkeypatch.setattr(shutil, 'which', lambda name: None)
        with pytest.raises(RuntimeError, match='Chrome'):
            _get_chrome_path()
