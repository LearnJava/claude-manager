// Text of the live status line under the feed (VIEW-TASKS.md UI-12).
// Pure selection logic plus a clock store that only ticks while subscribed,
// so a hidden or idle session holds no timers.

import { readable } from 'svelte/store';
import { toolDisplay, type ToolDisplayInput, type ToolLang } from './toolDisplay';

export const SPINNER_FRAMES = ['⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'];
export const SPINNER_FRAME_MS = 100;
export const VERB_ROTATE_MS = 4000;

const THINKING_VERBS: Record<ToolLang, string[]> = {
    ru: ['Размышляю', 'Анализирую', 'Прикидываю', 'Сопоставляю', 'Формулирую'],
    en: ['Pondering', 'Reasoning', 'Analyzing', 'Synthesizing', 'Considering'],
};
const WRITING: Record<ToolLang, string> = { ru: 'Пишет ответ', en: 'Writing the answer' };
const PREPARING: Record<ToolLang, string> = { ru: 'Готовлю', en: 'Preparing' };

export type ActivityKind = 'thinking' | 'tool' | 'writing' | 'idle';

// Whole seconds while live: `4s`, `1m05s`.
export function formatLiveElapsed(ms: number): string {
    const s = Math.floor(Math.max(0, ms) / 1000);
    return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m${String(s % 60).padStart(2, '0')}s`;
}

export interface LiveActivity {
    kind: ActivityKind;
    tool?: string;
    since: string;
}

// One clock for every ticking element; ticks every SPINNER_FRAME_MS only while
// somebody is subscribed.
export const liveNow = readable(Date.now(), (set) => {
    set(Date.now());
    const id = setInterval(() => set(Date.now()), SPINNER_FRAME_MS);
    return () => clearInterval(id);
});

export function spinnerFrame(nowMs: number): string {
    return SPINNER_FRAMES[Math.floor(nowMs / SPINNER_FRAME_MS) % SPINNER_FRAMES.length];
}

// Thinking verb rotates every VERB_ROTATE_MS of elapsed time.
export function thinkingVerb(elapsedMs: number, lang: ToolLang): string {
    const list = THINKING_VERBS[lang] ?? THINKING_VERBS.en;
    return list[Math.floor(Math.max(0, elapsedMs) / VERB_ROTATE_MS) % list.length];
}

export interface LiveLine {
    text: string;
    elapsed: string;
}

// null = show nothing (idle, or no activity yet).
export function liveStatusLine(
    a: LiveActivity | undefined,
    nowMs: number,
    lang: ToolLang,
    openCall?: ToolDisplayInput | null,
): LiveLine | null {
    if (!a || a.kind === 'idle') return null;
    const since = Date.parse(a.since);
    const ms = isNaN(since) ? 0 : Math.max(0, nowMs - since);
    const elapsed = formatLiveElapsed(ms);
    switch (a.kind) {
        case 'thinking':
            return { text: `${thinkingVerb(ms, lang)}…`, elapsed };
        case 'writing':
            return { text: `✍ ${WRITING[lang]}…`, elapsed };
        case 'tool': {
            if (openCall) {
                const d = toolDisplay(openCall, lang);
                return { text: `${d.emoji} ${d.running}`, elapsed };
            }
            const d = toolDisplay({ tool_name: a.tool }, lang);
            return { text: `${d.emoji} ${PREPARING[lang]} ${a.tool ?? ''}…`.replace(/ +…/, '…'), elapsed };
        }
    }
    return null;
}

interface EntryLike extends ToolDisplayInput {
    level?: string;
    tool_use_id?: string;
}

// Last tool call that has no result yet and is not closed by a turn `result`.
export function lastOpenCall<T extends EntryLike>(entries: T[]): T | null {
    const answered = new Set<string>();
    let anonAnswers = 0;
    for (let i = entries.length - 1; i >= 0; i--) {
        const e = entries[i];
        const level = (e.level ?? '').toLowerCase();
        if (level === 'result') return null;
        if (level === 'tool_result' || level === 'error') {
            if (e.tool_use_id) answered.add(e.tool_use_id);
            else anonAnswers++;
        } else if (level === 'tool') {
            if (e.tool_use_id ? answered.has(e.tool_use_id) : anonAnswers-- > 0) continue;
            return e;
        }
    }
    return null;
}
