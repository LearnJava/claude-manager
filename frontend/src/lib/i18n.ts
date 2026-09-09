// UI language: "en" (default) or "ru". Purely a display concern — prompts
// sent to Claude, log data from the CLI, and code/comments are never
// translated (see "UI Localization" in CLAUDE.md).
//
// Persisted like the theme (stores/theme.ts): applied instantly from
// localStorage on boot, then confirmed/overridden once GetConfig() resolves
// and Settings.svelte calls setLocale(cfg.Settings.Language) — the same
// two-step pattern theme.ts/setTheme() uses. Defaults to English so a fresh
// install (and the Playwright test config, which pins language="en"
// explicitly) renders byte-identical text to before this feature existed.
import { derived, writable } from 'svelte/store';
import en from './locales/en';
import ru from './locales/ru';

export type Locale = 'en' | 'ru';

const STORAGE_KEY = 'cm.locale';

function readStored(): Locale | null {
    try {
        const v = localStorage.getItem(STORAGE_KEY);
        if (v === 'en' || v === 'ru') return v;
    } catch (_) {
        // localStorage may be unavailable (e.g. sandboxed contexts).
    }
    return null;
}

export const locale = writable<Locale>(readStored() ?? 'en');

locale.subscribe((v) => {
    try {
        localStorage.setItem(STORAGE_KEY, v);
    } catch (_) {
        /* ignore */
    }
});

export function setLocale(v: Locale) {
    locale.set(v);
}

const dictionaries: Record<Locale, Record<string, string>> = { en, ru };

type TranslateFn = (key: string, params?: Record<string, string | number>) => string;

function translate(loc: Locale, key: string, params?: Record<string, string | number>): string {
    const dict = dictionaries[loc] ?? dictionaries.en;
    // A key missing in the active locale falls back to English, then to the
    // key itself — a missing translation must never render blank.
    let str = dict[key] ?? dictionaries.en[key] ?? key;
    if (params) {
        for (const [k, v] of Object.entries(params)) {
            str = str.split(`{${k}}`).join(String(v));
        }
    }
    return str;
}

// $t('some.key') in a component; reactive to locale changes like any store.
export const t = derived(locale, ($locale): TranslateFn => {
    return (key, params) => translate($locale, key, params);
});
