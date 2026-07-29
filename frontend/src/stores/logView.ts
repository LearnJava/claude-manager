// How the log stream renders message bodies: formatted markdown (default) or
// raw text. Persisted like the theme so the choice survives a restart — it is
// a reading preference, not per-session state.

import { writable } from 'svelte/store';

const STORAGE_KEY = 'cm.logMarkdown';

function readStored(): boolean | null {
    try {
        const v = localStorage.getItem(STORAGE_KEY);
        if (v === '1') return true;
        if (v === '0') return false;
    } catch (_) {
        // localStorage may be unavailable (sandboxed contexts).
    }
    return null;
}

// Default: formatted. Raw is the escape hatch, not the norm.
export const logMarkdown = writable<boolean>(readStored() ?? true);

logMarkdown.subscribe((on) => {
    try {
        localStorage.setItem(STORAGE_KEY, on ? '1' : '0');
    } catch (_) {
        /* ignore */
    }
});

export function setLogMarkdown(on: boolean) {
    logMarkdown.set(on);
}

export function toggleLogMarkdown() {
    logMarkdown.update((v) => !v);
}
