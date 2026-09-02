# tests/conftest.py
# Werkzeug >= 3.0 dropped the module-level __version__ attribute that
# Flask 2.3's test client still reads. Expose it from package metadata so
# the Flask test-client fixtures run regardless of the installed werkzeug.
import importlib.metadata

import werkzeug

if not hasattr(werkzeug, '__version__'):
    try:
        werkzeug.__version__ = importlib.metadata.version('werkzeug')
    except importlib.metadata.PackageNotFoundError:
        werkzeug.__version__ = '0'
