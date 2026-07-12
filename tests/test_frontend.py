from pathlib import Path


INDEX_HTML = (
    Path(__file__).resolve().parents[1] / 'weibospider' / 'static' / 'index.html'
).read_text(encoding='utf-8')


def test_annotation_input_focus_is_deferred_until_mouseup_finishes():
    assert "sel.removeAllRanges();\n  showAnnotationInput(" in INDEX_HTML
    assert "requestAnimationFrame(() => textarea.focus());" in INDEX_HTML


def test_shift_enter_saves_annotation_while_enter_keeps_default_newline():
    assert "textarea.addEventListener('keydown', e => {" in INDEX_HTML
    assert "if (e.key === 'Enter' && e.shiftKey)" in INDEX_HTML
    assert "e.preventDefault();\n      saveAnnotation();" in INDEX_HTML
