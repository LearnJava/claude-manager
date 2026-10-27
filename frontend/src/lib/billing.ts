// Subscription readouts (WASTE-TASKS.md WS-02).
//
// On a subscription the scarce thing is not money but the rate-limit window
// and time: when the limit runs out the work stops, whatever the run "cost" at
// list prices. In that mode every cost readout leads with tokens counted
// against the limit and with time, and the dollar figure steps back as a
// comparison unit. In 'api' mode none of this applies and the readouts behave
// exactly as they did before the setting existed.
//
// Everything here is pure so the two modes can be tested without a browser:
// the same numbers feed both, only labels and order differ.

import { tokenSplit } from './formatters';

export type BillingMode = 'subscription' | 'api';

export function normaliseBilling(v: unknown): BillingMode {
    return v === 'api' ? 'api' : 'subscription';
}

// Tokens a run took out of the limit window: fresh input plus cache write plus
// cache read. Output is left out on purpose — the window is metered on what is
// sent to the model, and output is already the smaller, separately priced part.
export function limitTokens(src: Record<string, any> | undefined | null): number {
    const s = tokenSplit(src);
    return s.input + s.cacheCreation + s.cacheRead;
}

// Fraction of the window `tokens` took, or null when the window size is
// unknown. Null, not 0: "0% of the window" and "no idea how big the window is"
// are different statements and must never render alike.
export function windowShare(tokens: number, windowTokens: number | undefined | null): number | null {
    const w = Number(windowTokens);
    if (!w || isNaN(w) || w <= 0) return null;
    const n = Number(tokens);
    if (!n || isNaN(n) || n < 0) return 0;
    return n / w;
}

// "23%" / "<1%" / "130%"; null renders as `unknown` (the caller's translated
// "share unknown"). Never prints "0%" for a non-zero spend.
export function formatWindowShare(share: number | null, unknown: string): string {
    if (share === null) return unknown;
    if (share <= 0) return '0%';
    const pct = share * 100;
    if (pct < 1) return '<1%';
    return `${Math.round(pct)}%`;
}

// Order of the cost readouts, first = most prominent. 'api' keeps the old
// dollars/tokens switch; 'subscription' always leads with the window and time.
export type ReadoutKey = 'window' | 'time' | 'tokens' | 'usd';

export function readoutOrder(mode: BillingMode, unit: 'tokens' | 'usd'): ReadoutKey[] {
    if (mode === 'subscription') return ['window', 'time', 'usd'];
    return unit === 'usd' ? ['usd', 'tokens'] : ['tokens', 'usd'];
}
