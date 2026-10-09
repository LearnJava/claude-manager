/**
 * lib/askUserMarker.ts — folding of the end-of-session "done" marker
 * (```ask-user block with kind "continue_session") into one log line.
 */

import { test, expect } from '@playwright/test';
import { doneMarkerSummary, foldDoneMarkers } from '../src/lib/askUserMarker';

const label = (s: string) => `[done${s ? ': ' + s : ''}]`;

test.describe('doneMarkerSummary', () => {
    test('continue_session body -> its summary', () => {
        expect(doneMarkerSummary('{"question": "LN-17 merged", "options": [], "kind": "continue_session"}')).toBe(
            'LN-17 merged',
        );
    });
    test('missing question -> empty summary', () => {
        expect(doneMarkerSummary('{"kind": "continue_session"}')).toBe('');
    });
    test('genuine decision, prose, broken JSON -> null', () => {
        expect(doneMarkerSummary('{"question": "Which?", "options": ["a", "b"]}')).toBeNull();
        expect(doneMarkerSummary('Which one?\n- a\n- b')).toBeNull();
        expect(doneMarkerSummary('{not json')).toBeNull();
    });
});

test.describe('foldDoneMarkers', () => {
    test('folds the old two-option form as well as the new one', () => {
        const src = [
            'All done.',
            '',
            '```ask-user',
            '{"question": "Task done. Start the next?", "options": ["Continue in this session", "Stop"], "kind": "continue_session"}',
            '```',
        ].join('\n');
        expect(foldDoneMarkers(src, label)).toBe('All done.\n\n[done: Task done. Start the next?]');
    });
    test('CRLF line endings', () => {
        const src = 'x\r\n```ask-user\r\n{"question": "q", "kind": "continue_session"}\r\n```';
        expect(foldDoneMarkers(src, label)).toBe('x\r\n[done: q]');
    });
    test('leaves a genuine-decision block untouched', () => {
        const src = '```ask-user\n{"question": "Which?", "options": ["a", "b"]}\n```';
        expect(foldDoneMarkers(src, label)).toBe(src);
    });
    test('no marker -> same string', () => {
        expect(foldDoneMarkers('```go\nx\n```', label)).toBe('```go\nx\n```');
        expect(foldDoneMarkers('', label)).toBe('');
    });
});
