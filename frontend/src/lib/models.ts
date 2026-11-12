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
 * A provider (a non-Anthropic brain behind the same Claude CLI) as the
 * backend's GetProviders reports it. The provider id '' (plain Claude) is
 * not listed by the backend — the picker adds it as the default segment.
 */
export interface ProviderModelUI {
    id: string;
    label: string;
    efforts: string[];
}

export interface ProviderUI {
    id: string;
    label: string;
    models: ProviderModelUI[];
    /** The hermes CLI --provider this brain maps to ("" = claude-only). */
    hermesProviderId?: string;
    /** The provider part of the opencode `provider/model` selector ("" = not offered to opencode). */
    opencodeProviderId?: string;
}

/** One flat "model × effort" row of a provider's picker list. */
export interface ProviderComboUI {
    value: string; // "<model id>::<effort>" — the <select> value
    label: string; // "GLM-5.3 low" — what the owner asked to see
    model: string;
    effort: string;
}

/** Flattens a provider's models × efforts into its picker list. */
export function providerCombos(p: ProviderUI): ProviderComboUI[] {
    const out: ProviderComboUI[] = [];
    for (const m of p.models ?? []) {
        for (const e of m.efforts ?? []) {
            out.push({ value: `${m.id}::${e}`, label: `${m.label} ${e}`, model: m.id, effort: e });
        }
    }
    return out;
}

/**
 * The picker list for an opencode session: the union of every
 * opencode-capable provider's models × efforts, where the model value is
 * the full `provider/model` selector the runtime passes to `--model` (the
 * effort rides along as the `#variant` suffix the runtime appends).
 */
export function openCodeCombos(providers: ProviderUI[]): ProviderComboUI[] {
    const out: ProviderComboUI[] = [];
    for (const p of providers.filter((x) => x.opencodeProviderId)) {
        for (const m of p.models ?? []) {
            for (const e of m.efforts ?? []) {
                const id = `${p.opencodeProviderId}/${m.id}`;
                out.push({ value: `${id}::${e}`, label: `${m.label} ${e}`, model: id, effort: e });
            }
        }
    }
    return out;
}

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
