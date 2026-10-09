// Folding of the end-of-session "done" marker in log text.
//
// An autonomous run ends its last reply with a fenced ```ask-user block whose
// JSON has "kind": "continue_session" (askUserProtocolPrompt,
// internal/session/session.go). It is not a question — the backend resolves
// it instantly — so showing the raw JSON in the log reads as an unanswered
// question. foldDoneMarkers swaps each such block for one human line; any
// other ask-user block (a genuine decision, shown in QuestionBanner) and any
// block that does not parse is left untouched.

const ASK_USER_BLOCK = /```ask-user[ \t]*\r?\n([\s\S]*?)\r?\n?```/g;

export const KIND_CONTINUE_SESSION = 'continue_session';

/** Summary text of a continue_session marker body, or null if the body is not one. */
export function doneMarkerSummary(body: string): string | null {
    let parsed: unknown;
    try {
        parsed = JSON.parse(body.trim());
    } catch {
        return null;
    }
    if (!parsed || typeof parsed !== 'object') return null;
    const obj = parsed as { kind?: unknown; question?: unknown };
    if (obj.kind !== KIND_CONTINUE_SESSION) return null;
    return typeof obj.question === 'string' ? obj.question.trim() : '';
}

/**
 * Replace every continue_session ask-user block in text with `label(summary)`.
 * Returns the input unchanged when there is nothing to fold.
 */
export function foldDoneMarkers(text: string, label: (summary: string) => string): string {
    if (!text || !text.includes('```ask-user')) return text;
    return text.replace(ASK_USER_BLOCK, (block: string, body: string) => {
        const summary = doneMarkerSummary(body);
        return summary === null ? block : label(summary);
    });
}
