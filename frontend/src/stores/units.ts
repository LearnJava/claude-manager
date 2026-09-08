// Which unit the cost readouts lead with: token volume or US dollars.
//
// Tokens are the default. On a subscription the dollar figure is a price-list
// calculation of something already paid for, while tokens are what the rate
// limit actually meters — "how much more can I get done today" is a token
// question, not a dollar one. Dollars stay available (and stay the right unit
// when comparing models or working off an API key), so this is a toggle, not a
// removal.
//
// Persisted like the theme and the log's markdown switch: it is a reading
// preference, not per-session state.

import { writable } from 'svelte/store';

export type CostUnit = 'tokens' | 'usd';

const STORAGE_KEY = 'cm.costUnit';

function readStored(): CostUnit | null {
    try {
        const v = localStorage.getItem(STORAGE_KEY);
        if (v === 'tokens' || v === 'usd') return v;
    } catch (_) {
        // localStorage may be unavailable (sandboxed contexts).
    }
    return null;
}

export const costUnit = writable<CostUnit>(readStored() ?? 'tokens');

costUnit.subscribe((u) => {
    try {
        localStorage.setItem(STORAGE_KEY, u);
    } catch (_) {
        /* ignore */
    }
});

export function setCostUnit(u: CostUnit) {
    costUnit.set(u);
}

export function toggleCostUnit() {
    costUnit.update((u) => (u === 'tokens' ? 'usd' : 'tokens'));
}
