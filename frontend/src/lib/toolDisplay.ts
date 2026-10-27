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

const HEREDOC_START = /^<<(-?)[ \t]*(?:'([^'\n]+)'|"([^"\n]+)"|\\?([A-Za-z_][A-Za-z0-9_]*))/;

interface PendingHeredoc {
    delim: string;
    stripTabs: boolean;
}

// Index just past the bodies of hs, which start at `pos` (the line after the
// heredoc operators). A body cut off by the ToolArgs length cap has no
// terminator and runs to the end of the string.
function skipHeredocBodies(cmd: string, pos: number, hs: PendingHeredoc[]): number {
    for (const h of hs) {
        for (;;) {
            if (pos >= cmd.length) return cmd.length;
            const nl = cmd.indexOf('\n', pos);
            const end = nl < 0 ? cmd.length : nl;
            let line = cmd.slice(pos, end).replace(/\r$/, '');
            if (h.stripTabs) line = line.replace(/^\t+/, '');
            pos = end + 1;
            if (line === h.delim) break;
        }
    }
    return pos;
}

// Splits a shell command into segments at && || ; and newlines, each segment
// into pipeline stages at |, the way bash reads it: separators inside quotes
// do not count, and heredoc bodies are skipped — their lines are data (often
// a whole script), not commands.
function splitShell(cmd: string): string[][] {
    const segs: string[][] = [];
    let stages: string[] = [];
    let cur = '';
    let pending: PendingHeredoc[] = [];
    const endStage = () => {
        stages.push(cur.trim());
        cur = '';
    };
    const endSeg = () => {
        endStage();
        segs.push(stages.filter(Boolean));
        stages = [];
    };
    for (let i = 0; i < cmd.length; i++) {
        const ch = cmd[i];
        if (ch === "'") {
            const j = cmd.indexOf("'", i + 1);
            const end = j < 0 ? cmd.length : j + 1;
            cur += cmd.slice(i, end);
            i = end - 1;
        } else if (ch === '"') {
            let j = i + 1;
            while (j < cmd.length && cmd[j] !== '"') j += cmd[j] === '\\' ? 2 : 1;
            const end = Math.min(j + 1, cmd.length);
            cur += cmd.slice(i, end);
            i = end - 1;
        } else if (ch === '\\') {
            cur += cmd.slice(i, i + 2);
            i++;
        } else if (ch === '<' && cmd[i + 1] === '<' && cmd[i + 2] !== '<') {
            const m = HEREDOC_START.exec(cmd.slice(i));
            if (m) {
                pending.push({ delim: m[2] ?? m[3] ?? m[4], stripTabs: m[1] === '-' });
                cur += m[0];
                i += m[0].length - 1;
            } else {
                cur += ch;
            }
        } else if (ch === '\n') {
            endSeg();
            if (pending.length > 0) {
                i = skipHeredocBodies(cmd, i + 1, pending) - 1;
                pending = [];
            }
        } else if (ch === ';') {
            endSeg();
        } else if (ch === '&' && cmd[i + 1] === '&') {
            endSeg();
            i++;
        } else if (ch === '|') {
            if (cmd[i + 1] === '|') {
                endSeg();
                i++;
            } else {
                endStage();
            }
        } else {
            cur += ch;
        }
    }
    endSeg();
    return segs.filter((s) => s.length > 0);
}

// One pipeline stage for display: no subshell/group brackets, no heredoc
// operator, no redirects, the program by its name rather than full path.
function cleanStage(stage: string): string {
    const s = stage
        .replace(/^[({]\s*/, '')
        .replace(/\s*[)}]$/, '')
        .replace(/<<-?[ \t]*(?:'[^']*'|"[^"]*"|\\?\w+)/g, ' ')
        .replace(/\d*>&\d+/g, ' ')
        .replace(/\d*>>?\s*\S+/g, ' ')
        .replace(/\s+/g, ' ')
        .trim();
    const sp = s.indexOf(' ');
    const prog = sp < 0 ? s : s.slice(0, sp);
    return /[\\/]/.test(prog) ? baseName(prog) + s.slice(prog.length) : s;
}

// Port of Hermes' summarize_shell_command: drops setup segments (cd/export/
// source/true), redirects and trailing head/tail/wc/sort/uniq stages, keeps
// the first remaining command and counts the rest ("npm test + 2"). A file
// written from a heredoc ("cat > x.py <<'EOF'") counts but does not lead when
// the call also runs something: "python x.py + 1" says more than "cat".
export function summarizeShellCommand(cmd: string): string {
    const kept: string[] = [];
    const writes: string[] = [];
    for (const seg of splitShell(cmd)) {
        const stages = seg.map(cleanStage).filter(Boolean);
        while (stages.length > 1 && PIPE_TAIL.has(stages[stages.length - 1].split(' ')[0])) stages.pop();
        if (stages.length === 0) continue;
        const first = stages[0].split(' ')[0];
        if (SHELL_SKIP.has(first)) continue;
        const target = /^(?:cat|tee)\b/.test(first) && seg[0].includes('<<') ? />>?\s*(\S+)/.exec(seg[0]) : null;
        if (target) writes.push(`${first} > ${target[1]}`);
        else kept.push(stages.join(' | '));
    }
    const all = [...kept, ...writes];
    if (all.length === 0) return clip(cmd);
    const head = clip(all[0]);
    return all.length > 1 ? `${head} + ${all.length - 1}` : head;
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
