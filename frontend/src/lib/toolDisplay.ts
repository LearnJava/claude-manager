// Human-readable description of a tool call — emoji, short verb, detail and a
// live "Reading main.go L10-49" phrase. Port of Hermes' _CUTE_LINES/_TOOL_VERBS
// for the tool names of both runtimes (Claude and Hermes). VIEW-TASKS.md UI-10.
//
// Pure module (no Svelte, no i18n store): the caller picks the language.

export interface ToolDisplayInput {
    tool_name?: string;
    tool_input?: string;
    tool_args?: Record<string, string>;
}

export interface ToolDisplay {
    emoji: string;
    // Short verb for the summary line: read, $, grep, …
    verb: string;
    // What the call operates on: file, command, pattern, …
    detail: string;
    // Phrase for the live line while the call runs.
    running: string;
}

export type ToolLang = 'ru' | 'en';

const MAX_PATH_CHARS = 60;
const MAX_DETAIL_CHARS = 80;

// Truncates from the start so the file name survives:
// "/very/long/internal/session/parser.go" -> "…/internal/session/parser.go".
export function shortenPath(path: string, max = MAX_PATH_CHARS): string {
    if (path.length <= max) return path;
    const tail = path.slice(path.length - (max - 1));
    const sep = tail.search(/[\\/]/);
    return '…' + (sep > 0 ? tail.slice(sep) : tail);
}

function baseName(path: string): string {
    const parts = path.trim().split(/[\\/]/).filter(Boolean);
    return parts.length > 0 ? parts[parts.length - 1] : path.trim();
}

function clip(s: string, max = MAX_DETAIL_CHARS): string {
    const one = s.trim().split('\n', 1)[0];
    return one.length > max ? one.slice(0, max) + '…' : one;
}

const SHELL_SKIP = new Set(['cd', 'export', 'source', 'true']);
const PIPE_TAIL = new Set(['head', 'tail', 'wc', 'sort', 'uniq']);

// Port of Hermes' summarize_shell_command: drops setup segments (cd/export/
// source/true), redirects and trailing head/tail/wc/sort/uniq stages, keeps
// the first remaining command and counts the rest ("npm test + 2").
export function summarizeShellCommand(cmd: string): string {
    const cleaned = cmd.replace(/\d*>&\d+/g, ' ').replace(/\d*>>?\s*\S+/g, ' ');
    const kept: string[] = [];
    for (const seg of cleaned.split(/&&|\|\||;|\n/)) {
        const stages = seg
            .split('|')
            .map((s) => s.trim())
            .filter(Boolean);
        while (stages.length > 1 && PIPE_TAIL.has(stages[stages.length - 1].split(/\s+/)[0])) stages.pop();
        if (stages.length === 0) continue;
        const first = stages[0].split(/\s+/)[0];
        if (SHELL_SKIP.has(first)) continue;
        kept.push(stages.join(' | ').replace(/\s+/g, ' '));
    }
    if (kept.length === 0) return clip(cmd);
    const head = clip(kept[0]);
    return kept.length > 1 ? `${head} + ${kept.length - 1}` : head;
}

function readRange(a: Record<string, string>): string {
    const off = parseInt(a.offset ?? '', 10);
    const lim = parseInt(a.limit ?? '', 10);
    if (!isNaN(off) && !isNaN(lim) && lim > 0) return ` L${off}-${off + lim - 1}`;
    if (!isNaN(lim) && lim > 0) return ` L1-${lim}`;
    if (!isNaN(off) && off > 1) return ` L${off}+`;
    return '';
}

const FALLBACK_KEYS = ['query', 'text', 'command', 'path', 'name', 'prompt'];

function firstArg(a: Record<string, string>, input: string): string {
    for (const k of FALLBACK_KEYS) if (a[k]) return clip(a[k]);
    return clip(input);
}

function domain(url: string): string {
    const m = url.trim().match(/^[a-z][a-z0-9+.-]*:\/\/([^/?#:]+)/i);
    return m ? m[1] : clip(url);
}

const RUNNING: Record<string, { ru: string; en: string }> = {
    read: { ru: 'Читаю', en: 'Reading' },
    write: { ru: 'Пишу', en: 'Writing' },
    edit: { ru: 'Правлю', en: 'Editing' },
    $: { ru: 'Выполняю', en: 'Running' },
    grep: { ru: 'Ищу', en: 'Searching' },
    find: { ru: 'Ищу файлы', en: 'Finding' },
    search: { ru: 'Ищу в сети', en: 'Searching' },
    fetch: { ru: 'Загружаю', en: 'Fetching' },
    plan: { ru: 'Обновляю план', en: 'Planning' },
    delegate: { ru: 'Делегирую', en: 'Delegating' },
    skill: { ru: 'Загружаю навык', en: 'Loading skill' },
};

function runningPhrase(verb: string, detail: string, lang: ToolLang, generic: boolean): string {
    if (generic) return [lang === 'ru' ? 'Вызываю' : 'Calling', verb, detail].filter(Boolean).join(' ');
    return [RUNNING[verb][lang], detail].filter(Boolean).join(' ');
}

export function toolDisplay(e: ToolDisplayInput, lang: ToolLang = 'en'): ToolDisplay {
    const name = e.tool_name ?? '';
    const a = e.tool_args ?? {};
    const input = (e.tool_input ?? '').trim();
    const path = a.file_path ?? a.path ?? a.notebook_path ?? input;

    let emoji = '⚡';
    let verb = name;
    let detail = '';
    let generic = false;

    switch (name) {
        case 'Read':
        case 'read_file':
            emoji = '📖';
            verb = 'read';
            detail = path ? baseName(path) + readRange(a) : '';
            break;
        case 'Write':
        case 'write_file':
            emoji = '✍️';
            verb = 'write';
            detail = shortenPath(path);
            break;
        case 'Edit':
        case 'MultiEdit':
        case 'patch':
            emoji = '🔧';
            verb = 'edit';
            detail = shortenPath(path);
            break;
        case 'Bash':
        case 'terminal':
            emoji = '💻';
            verb = '$';
            detail = a.description ? clip(a.description) : summarizeShellCommand(a.command ?? input);
            break;
        case 'Grep':
            emoji = '🔎';
            verb = 'grep';
            detail = a.pattern ? clip(a.pattern) + (a.path ? ` in ${shortenPath(a.path)}` : '') : clip(input);
            break;
        case 'Glob':
            emoji = '🔎';
            verb = 'find';
            detail = clip(a.pattern ?? input);
            break;
        case 'search_files':
            emoji = '🔎';
            if (a.target === 'files') {
                verb = 'find';
                detail = clip(a.pattern ?? input);
            } else {
                verb = 'grep';
                detail = a.pattern ? clip(a.pattern) + (a.path ? ` in ${shortenPath(a.path)}` : '') : clip(input);
            }
            break;
        case 'WebSearch':
        case 'web_search':
            emoji = '🔍';
            verb = 'search';
            detail = clip(a.query ?? input);
            break;
        case 'WebFetch':
        case 'web_extract':
            emoji = '📄';
            verb = 'fetch';
            detail = domain(a.url ?? a.urls ?? input);
            break;
        case 'TodoWrite':
        case 'todo_list': {
            emoji = '📋';
            verb = 'plan';
            const n = parseInt(a.todos ?? '', 10);
            detail = isNaN(n) ? '' : lang === 'ru' ? `${n} задач` : `${n} tasks`;
            break;
        }
        case 'Agent':
        case 'Task':
        case 'delegate_task':
            emoji = '🔀';
            verb = 'delegate';
            detail = clip(a.description ?? a.goal ?? input);
            break;
        case 'Skill':
        case 'skill_view':
            emoji = '📚';
            verb = 'skill';
            detail = clip(a.skill ?? a.name ?? input);
            break;
        default: {
            generic = true;
            const mcp = name.match(/^mcp__(.+?)__(.+)$/);
            if (mcp) {
                emoji = '🧩';
                verb = mcp[1];
                const arg = firstArg(a, input);
                detail = arg ? `${mcp[2]} ${arg}` : mcp[2];
            } else {
                detail = firstArg(a, input);
            }
        }
    }

    return { emoji, verb, detail, running: runningPhrase(verb, detail, lang, generic) };
}
