// How tokens are paid for (config [settings] billing / limit_window_tokens).
//
// Persisted like the language: applied instantly from localStorage on boot so
// the first paint already has the right units, then confirmed from the config
// once GetConfig() resolves (App.svelte) and again whenever Settings saves.
// Defaults to subscription, matching the Go-side default.

import { writable } from 'svelte/store';
import { normaliseBilling, type BillingMode } from '../lib/billing';

export interface BillingSettings {
    mode: BillingMode;
    // Size of the limit window in tokens; 0 = unknown.
    windowTokens: number;
}

const STORAGE_KEY = 'cm.billing';

function readStored(): BillingSettings | null {
    try {
        const raw = localStorage.getItem(STORAGE_KEY);
        if (!raw) return null;
        const v = JSON.parse(raw);
        return { mode: normaliseBilling(v?.mode), windowTokens: Math.max(0, Number(v?.windowTokens) || 0) };
    } catch (_) {
        // localStorage may be unavailable, or hold something else.
        return null;
    }
}

export const billing = writable<BillingSettings>(readStored() ?? { mode: 'subscription', windowTokens: 0 });

billing.subscribe((v) => {
    try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(v));
    } catch (_) {
        /* ignore */
    }
});

// Takes the Go-exported GlobalSettings (PascalCase) as GetConfig returns it.
export function applyBillingFromSettings(gs: any) {
    billing.set({
        mode: normaliseBilling(gs?.Billing),
        windowTokens: Math.max(0, Number(gs?.LimitWindowTokens) || 0),
    });
}
