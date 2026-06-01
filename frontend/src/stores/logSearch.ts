import { writable } from 'svelte/store';

// Filter text applied to the visible log entries in <LogStream>. Empty string
// means "show everything".
export const logSearchText = writable<string>('');

// Bumped every time the user invokes Ctrl+F — LogStream watches this counter
// and focuses its input field on change.
export const logSearchFocus = writable<number>(0);

export const logSearch = {
    setText(value: string) {
        logSearchText.set(value);
    },
    clear() {
        logSearchText.set('');
    },
    focus() {
        logSearchFocus.update((n) => n + 1);
    },
};
