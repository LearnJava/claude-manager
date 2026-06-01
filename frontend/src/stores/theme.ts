import { writable } from 'svelte/store';

export type Theme = 'dark' | 'light';

const STORAGE_KEY = 'cm.theme';

function readStored(): Theme | null {
    try {
        const v = localStorage.getItem(STORAGE_KEY);
        if (v === 'dark' || v === 'light') return v;
    } catch (_) {
        // localStorage may be unavailable (e.g. SSR or sandboxed contexts).
    }
    return null;
}

function applyTheme(t: Theme) {
    const el = document.documentElement;
    if (t === 'dark') el.classList.add('dark');
    else el.classList.remove('dark');
}

// Defaults to dark to match the existing UI tokens.
const initial: Theme = readStored() ?? 'dark';

export const theme = writable<Theme>(initial);

theme.subscribe((t) => {
    applyTheme(t);
    try {
        localStorage.setItem(STORAGE_KEY, t);
    } catch (_) {
        /* ignore */
    }
});

export function setTheme(t: Theme) {
    theme.set(t);
}

export function toggleTheme() {
    theme.update((t) => (t === 'dark' ? 'light' : 'dark'));
}
