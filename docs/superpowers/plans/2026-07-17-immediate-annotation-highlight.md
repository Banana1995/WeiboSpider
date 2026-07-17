# Immediate Annotation Highlight Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Highlight selected tweet text immediately after its pending annotation is created, while keeping the current save and cancel behavior.

**Architecture:** Reuse the existing `applyHighlights(tweetId, anns)` renderer with a temporary annotation object assembled from the POST response ID and the selection data already available in `createPendingAnnotation`. Do not reload the annotation panel until save or cancel, so the empty pending annotation is not rendered as a second card.

**Tech Stack:** Vanilla JavaScript in `index.html`, Flask annotation API, pytest source-level frontend tests.

---

## File Structure

- Modify `tests/test_frontend.py`: assert that pending annotation creation invokes the existing highlight renderer before the input is shown, preserves JSON-formatted cross-field ranges, and does not reload the panel at creation time.
- Modify `weibospider/static/index.html:1102-1116`: construct the pending annotation object and call `applyHighlights` before `showAnnotationInput`.

### Task 1: Add Immediate Pending Highlight

**Files:**
- Modify: `tests/test_frontend.py:115-119`
- Modify: `weibospider/static/index.html:1102-1116`

- [ ] **Step 1: Write the failing frontend test**

Append these tests to `tests/test_frontend.py`:

```python
def test_pending_annotation_highlights_before_showing_input():
    create_start = INDEX_HTML.index('async function createPendingAnnotation(')
    create_end = INDEX_HTML.index('\nfunction findFieldElement(', create_start)
    create_body = INDEX_HTML[create_start:create_end]

    assert "const pendingAnnotation = {" in create_body
    assert "id: data.id" in create_body
    assert "field, start_offset: startOffset, end_offset: endOffset" in create_body
    assert "ranges: ranges ? JSON.stringify(ranges) : null" in create_body
    assert "applyHighlights(tweetId, [pendingAnnotation]);" in create_body
    assert create_body.index("applyHighlights(tweetId, [pendingAnnotation]);") < create_body.index("showAnnotationInput(")


def test_pending_annotation_creation_does_not_reload_panel():
    create_start = INDEX_HTML.index('async function createPendingAnnotation(')
    create_end = INDEX_HTML.index('\nfunction findFieldElement(', create_start)
    create_body = INDEX_HTML[create_start:create_end]

    assert "loadAnnotations(tweetId)" not in create_body
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `python3 -m pytest tests/test_frontend.py::test_pending_annotation_highlights_before_showing_input tests/test_frontend.py::test_pending_annotation_creation_does_not_reload_panel -q`

Expected: the first test fails because `pendingAnnotation` and `applyHighlights(tweetId, [pendingAnnotation])` do not exist yet; the second test passes and protects against reintroducing the duplicate empty card.

- [ ] **Step 3: Implement the minimal pending-highlight flow**

Replace the success branch in `createPendingAnnotation` in `weibospider/static/index.html`:

```javascript
    const data = await resp.json();
    if (resp.ok) {
      const pendingAnnotation = {
        id: data.id,
        field, start_offset: startOffset, end_offset: endOffset,
        selected_text: selectedText,
        ranges: ranges ? JSON.stringify(ranges) : null,
      };
      applyHighlights(tweetId, [pendingAnnotation]);
      showAnnotationInput(data.id, tweetId, field, startOffset, endOffset, selectedText, ranges);
    }
```

This keeps the annotation panel untouched, renders single-field and cross-field pending highlights through the existing offset logic, and leaves save/cancel to call the existing `loadAnnotations` paths.

- [ ] **Step 4: Run the focused frontend tests**

Run: `python3 -m pytest tests/test_frontend.py -q`

Expected: all frontend tests pass.

- [ ] **Step 5: Run the full test suite**

Run: `python3 -m pytest -q`

Expected: all tests pass with no regressions.

- [ ] **Step 6: Manually verify browser behavior**

Run: `python3 weibospider/run.py --dev --port 5050`

Verify in `http://localhost:5050`:

1. Select text within one field: it highlights immediately and only one comment input appears.
2. Select across multiple annotated fields: every selected range highlights immediately.
3. Enter a comment and save: the highlight remains and the saved annotation card appears.
4. Select another range and cancel: the temporary highlight disappears and the empty annotation is deleted.
5. Confirm the browser console contains no JavaScript errors.

- [ ] **Step 7: Commit the implementation**

```bash
git add tests/test_frontend.py weibospider/static/index.html
git commit -m "fix: highlight annotation immediately on selection"
```
