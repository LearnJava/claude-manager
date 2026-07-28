/**
 * Single source of truth for the model names the UI offers and displays.
 *
 * Two different vocabularies used to leak into the same dropdown: the CLI
 * aliases we pass to `--model` (`haiku`/`sonnet`/`opus`) and the fully-resolved
 * ids the CLI reports back in its `system/init` event (`claude-sonnet-5`,
 * `claude-haiku-4-5-20251001`, …). The sidebar appended the resolved id as an
 * extra <option>, so one list showed both "sonnet" and "claude-sonnet-5" —
 * the same model under two names, plus a third spelling in Settings.
 *
 * `normalizeModel` folds a resolved id back onto the alias we send, so a
 * `<select>` value always matches one of MODELS; `modelLabel` renders it the
 * same way in every component.
 */

export interface ModelOption {
    value: string;
    label: string;
}

/**
 * Values are what actually goes to `--model`. Aliases are preferred where the
 * CLI has one (they always resolve to the latest snapshot); Fable has no
 * alias, so its full id is the canonical value.
 */
export const MODELS: ModelOption[] = [
    { value: 'haiku', label: 'Haiku' },
    { value: 'sonnet', label: 'Sonnet' },
    { value: 'opus', label: 'Opus' },
    { value: 'claude-fable-5', label: 'Fable' },
];

export const EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max'];

/**
 * Maps any spelling of a model onto the canonical value from MODELS.
 * An unrecognised model is returned trimmed but otherwise untouched — a
 * custom/pinned id the user typed in Settings must still round-trip.
 */
export function normalizeModel(raw: string | null | undefined): string {
    const trimmed = (raw ?? '').trim();
    const m = trimmed.toLowerCase();
    if (!m) return '';
    if (m.includes('haiku')) return 'haiku';
    if (m.includes('sonnet')) return 'sonnet';
    if (m.includes('opus')) return 'opus';
    if (m.includes('fable')) return 'claude-fable-5';
    return trimmed;
}

/** Display name for a model, falling back to the raw value when unknown. */
export function modelLabel(raw: string | null | undefined): string {
    const value = normalizeModel(raw);
    return MODELS.find((o) => o.value === value)?.label ?? value;
}

/** True when raw resolves to one of the offered options. */
export function isKnownModel(raw: string | null | undefined): boolean {
    const value = normalizeModel(raw);
    return MODELS.some((o) => o.value === value);
}
