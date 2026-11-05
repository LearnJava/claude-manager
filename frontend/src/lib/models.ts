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
 * What each CLI backend actually supports (mirrors session.Capabilities in
 * internal/session/runtime.go). UI components key on these flags instead of
 * hardcoding runtime names: a per-turn runtime cannot be interrupted, one
 * without effort hides the effort picker, one without cost shows "no data"
 * instead of $0.
 */
export interface RuntimeCapabilities {
    resume: boolean;
    effort: boolean;
    permissionProtocol: boolean;
    cost: boolean;
    tokenUsage: boolean;
    images: boolean;
    perTurnProcess: boolean;
}

export const RUNTIME_CAPABILITIES: Record<string, RuntimeCapabilities> = {
    claude: { resume: true, effort: true, permissionProtocol: true, cost: true, tokenUsage: true, images: true, perTurnProcess: false },
    hermes: { resume: true, effort: true, permissionProtocol: false, cost: false, tokenUsage: true, images: true, perTurnProcess: true },
    kimi: { resume: true, effort: false, permissionProtocol: false, cost: false, tokenUsage: false, images: false, perTurnProcess: true },
    // Mirrors zcodeRuntime.Capabilities() (internal/session/zcode_runtime.go):
    // resume via the order's conversation_id, permissions answered through
    // the order file, usage reported by the outbox; effort rides the order
    // file as reasoning_effort (applying it is up to the consuming ZCode
    // session); no USD cost, no image attachments on the file wire.
    zcode: { resume: true, effort: true, permissionProtocol: true, cost: false, tokenUsage: true, images: false, perTurnProcess: true },
};

export function capabilitiesOf(runtime: string | null | undefined): RuntimeCapabilities {
    return RUNTIME_CAPABILITIES[runtime ?? ''] ?? RUNTIME_CAPABILITIES.claude;
}

/** Model catalog for the Kimi Code CLI (`-m` alias from its own config). */
export const KIMI_MODELS: ModelOption[] = [
    { value: 'kimi-code/kimi-for-coding', label: 'K2.8 Preview' },
    { value: 'kimi-code/kimi-for-coding-highspeed', label: 'K2.7 Code Highspeed' },
    { value: 'kimi-code/k3', label: 'K3' },
    { value: 'kimi-code/k3-256k', label: 'K3-256k' },
    { value: 'kimi-code/k3.1-pro', label: 'K3.1 Pro' },
    { value: 'kimi-code/k3.1-pro-highspeed', label: 'K3.1 Pro Highspeed 2x Usage Cost' },
];

/**
 * Model catalog for ZCode: the names its own model picker shows (Z.ai
 * account, checked 2026-11-05). Values travel verbatim into the order
 * file's `model` field (internal/session/zcode_file.go) — no alias
 * resolution, and a pinned id from config.toml still round-trips as a
 * custom option.
 */
export const ZCODE_MODELS: ModelOption[] = [
    { value: 'GLM-5.3', label: 'GLM-5.3' },
    { value: 'GLM-5.3-Flash', label: 'GLM-5.3-Flash' },
];

/** MODELS for Claude/Hermes, KIMI_MODELS for Kimi, ZCODE_MODELS for ZCode. */
export function modelsFor(runtime: string | null | undefined): ModelOption[] {
    const r = runtime ?? '';
    if (r === 'kimi') return KIMI_MODELS;
    if (r === 'zcode') return ZCODE_MODELS;
    return MODELS;
}

/**
 * Reasoning-effort levels the ZCode side actually has: GLM-5.3 officially
 * supports low / high / max (default max; thinking cannot be disabled) —
 * the shared EFFORTS list carries Claude/Hermes values that GLM lacks.
 */
export const EFFORTS_ZCODE = ['low', 'high', 'max'];

/** EFFORTS_ZCODE for ZCode, the shared EFFORTS list otherwise. */
export function effortsFor(runtime: string | null | undefined): string[] {
    if ((runtime ?? '') === 'zcode') return EFFORTS_ZCODE;
    return EFFORTS;
}

// Superset of every runtime's levels, ordered weakest → strongest; snapEffort
// uses it to map a level one list lacks onto the nearest one it has.
const EFFORT_ORDER = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'];

/**
 * Snap an effort to the runtime's list: the nearest listed level at or above
 * the requested one, else the strongest listed level. A recommendation
 * computed for one runtime (e.g. 'medium' from the Claude router) stays a
 * valid choice when the launch targets another (GLM-5.3 has no 'medium').
 */
export function snapEffort(runtime: string | null | undefined, effort: string): string {
    const list = effortsFor(runtime);
    if (list.includes(effort)) return effort;
    const at = EFFORT_ORDER.indexOf(effort);
    return list.find((e) => EFFORT_ORDER.indexOf(e) >= at) ?? list[list.length - 1];
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

/** Display name in the runtime's own catalog, falling back to the raw value. */
export function modelLabelFor(runtime: string | null | undefined, raw: string | null | undefined): string {
    // Kimi and ZCode take exact ids — no alias folding onto the Claude list.
    const exact = (runtime ?? '') === 'kimi' || (runtime ?? '') === 'zcode';
    const value = exact ? (raw ?? '').trim() : normalizeModel(raw);
    return modelsFor(runtime).find((o) => o.value === value)?.label ?? value;
}

/** Display name for a model, falling back to the raw value when unknown. */
export function modelLabel(raw: string | null | undefined): string {
    return modelLabelFor('', raw);
}

/** True when raw resolves to one of the offered options. */
export function isKnownModel(raw: string | null | undefined): boolean {
    const value = normalizeModel(raw);
    return MODELS.some((o) => o.value === value);
}
