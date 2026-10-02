// Clipboard write that reports success instead of throwing — callers flip a
// button to a ✓ / ✖ state rather than surfacing an exception.
export async function copyText(text: string): Promise<boolean> {
    try {
        await navigator.clipboard.writeText(text);
        return true;
    } catch {
        return false;
    }
}

// How long a copy button shows its ✓ / ✖ result before reverting.
export const COPY_FEEDBACK_MS = 1500;
