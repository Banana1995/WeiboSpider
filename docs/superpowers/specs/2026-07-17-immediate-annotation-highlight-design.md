# Immediate Annotation Highlight Design

## Goal

Highlight selected tweet text as soon as the pending annotation is created, without waiting for the user to save a comment.

## Current Behavior

The selection handler posts an empty annotation and then displays the comment input. Existing highlights are rendered only by `loadAnnotations`, which is called after save or cancel. Calling `loadAnnotations` immediately would also render the empty annotation as a second "未输入评论" card, which was removed in commit `800c137`.

## Design

Reuse the existing `applyHighlights` function after the annotation POST succeeds:

1. The user selects text and the frontend posts an empty annotation.
2. On a successful response, the frontend constructs one annotation object from the returned ID and the selection data already held by `createPendingAnnotation`.
3. The frontend calls `applyHighlights(tweetId, [pendingAnnotation])` before showing the comment input.
4. The annotation panel is not reloaded at this point, so no empty annotation card is rendered.
5. Saving calls the existing `loadAnnotations(tweetId)` path, replacing the pending highlight with highlights based on persisted annotations.
6. Cancelling deletes the empty annotation and calls `loadAnnotations(tweetId)`, which removes the pending highlight and restores highlights for any remaining annotations.

The pending object includes `id`, `field`, `start_offset`, `end_offset`, `selected_text`, and `ranges`. For cross-field selections, `ranges` remains the JSON string expected by `applyHighlights`.

## Error Handling

- If annotation creation fails, no highlight is applied.
- If cancellation deletion succeeds, the existing reload removes the temporary highlight.
- If cancellation deletion or reload fails, the existing request error behavior remains unchanged; no new persistence behavior is introduced.

## Scope

- Change only the annotation creation flow in `weibospider/static/index.html`.
- Reuse the existing highlight renderer rather than introducing a second temporary-range implementation.
- Preserve existing single-field and cross-field selection behavior.
- Preserve the current behavior where cancel removes both the empty annotation and its highlight.

## Verification

- Single-field selection highlights immediately and shows one input panel.
- Cross-field selection highlights all selected ranges immediately.
- Save retains the highlight and renders the saved annotation card.
- Cancel removes the highlight and the empty annotation.
- Creation failure leaves the text unhighlighted.
