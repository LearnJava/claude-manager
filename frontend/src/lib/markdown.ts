// Minimal, dependency-free Markdown → HTML renderer for the log view.
//
// Why hand-rolled instead of `marked`/`markdown-it`: the log is the only place
// that needs markdown, the app ships as an offline desktop build, and the
// output is injected with {@html} — so every byte of HTML has to be produced
// here, never passed through from the model. Everything that is not a tag this
// file emits itself is escaped first (see `escapeHtml`), which makes the
// renderer safe by construction rather than by a sanitizer pass afterwards.
//
// Supported: fenced/indented-free code blocks, ATX headings, horizontal rules,
// blockquotes, ordered/unordered/task lists (nested by indent), pipe tables,
// paragraphs with soft line breaks, and inline code / bold / italic /
// strikethrough / links / bare URLs.

const ESCAPES: Record<string, string> = {
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;',
};

export function escapeHtml(s: string): string {
    return String(s ?? '').replace(/[&<>"']/g, (c) => ESCAPES[c]);
}

// ---- Detection -------------------------------------------------------------

// Markup that makes a message worth rendering as markdown. Deliberately
// conservative: a false positive turns a plain log line into a heading or a
// list, which is worse than leaving it as text.
const MARKDOWN_SIGNALS: RegExp[] = [
    /^\s{0,3}(```|~~~)/m,               // fenced code
    /^\s{0,3}#{1,6}\s+\S/m,             // ATX heading
    /^\s{0,3}([-*+]|\d{1,9}[.)])\s+\S/m, // list item
    /^\s{0,3}>\s?\S/m,                  // blockquote
    /^\s{0,3}\|.*\|/m,                  // table row
    /^\s{0,3}([-*_])\s*(\1\s*){2,}$/m,  // horizontal rule
    /\*\*[^*\n]+\*\*/,                  // bold
    /(^|\s)_[^_\n]+_(\s|$)/,            // italic
    /~~[^~\n]+~~/,                      // strikethrough
    /`[^`\n]+`/,                        // inline code
    /\[[^\]\n]*\]\([^)\s]+\)/,          // link
];

export function hasMarkdown(text: string | undefined | null): boolean {
    const s = String(text ?? '');
    if (!s.trim()) return false;
    return MARKDOWN_SIGNALS.some((re) => re.test(s));
}

// ---- Inline ----------------------------------------------------------------

// Only these schemes may end up in an href. Anything else (javascript:, data:,
// vbscript:) is dropped and the link degrades to its label text.
const SAFE_URL = /^(?:https?:\/\/|mailto:|#|\.{0,2}\/)/i;

function safeUrl(url: string): string | null {
    const u = url.trim();
    if (!u) return null;
    if (SAFE_URL.test(u)) return u;
    if (/^www\./i.test(u)) return 'https://' + u;
    return null;
}

function anchorOpen(url: string): string {
    // target=_blank so a click opens the system browser instead of navigating
    // the app's WebView away from the UI.
    return `<a href="${url}" target="_blank" rel="noreferrer noopener">`;
}

export function renderInline(src: string): string {
    const stash: string[] = [];
    // Placeholders use control characters, which cannot appear in the rendered
    // HTML and are untouched by escaping and by the emphasis regexes below.
    const keep = (html: string): string => {
        stash.push(html);
        return `\u0000${stash.length - 1}\u0001`;
    };

    // 1. Code spans first — their contents must not be re-processed.
    let s = String(src ?? '').replace(/`([^`\n]+)`/g, (_m, code: string) =>
        keep(`<code>${escapeHtml(code)}</code>`),
    );

    // 2. Everything that is left is untrusted text.
    s = escapeHtml(s);

    // 3. Links (images are rendered as plain links — the log never loads remote
    //    resources). The open/close tags are stashed but the label stays in the
    //    stream so emphasis inside it is still rendered.
    s = s.replace(
        // The URL may contain one level of balanced parens (…/alert(1)) so an
        // unsafe link is dropped whole instead of leaving a stray ")" behind.
        /!?\[([^\]\n]*)\]\(\s*((?:[^()\s]|\([^()\s]*\))+)[^)\n]*\)/g,
        (m: string, label: string, url: string) => {
            const href = safeUrl(url);
            if (!href) return label || m;
            return keep(anchorOpen(href)) + (label || href) + keep('</a>');
        },
    );

    // 4. Bare URLs.
    s = s.replace(
        /(^|[\s(])((?:https?:\/\/|www\.)[^\s<>()]+[^\s<>().,;:!?'"])/g,
        (_m, lead: string, url: string) => {
            const href = safeUrl(url);
            if (!href) return _m;
            return lead + keep(anchorOpen(href)) + url + keep('</a>');
        },
    );

    // 5. Emphasis — bold before italic so `**x**` is not eaten by `*x*`.
    s = s.replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>');
    s = s.replace(/__([^_\n]+)__/g, '<strong>$1</strong>');
    s = s.replace(/~~([^~\n]+)~~/g, '<del>$1</del>');
    s = s.replace(/(^|[^*\w])\*([^*\n]+)\*(?!\*)/g, '$1<em>$2</em>');
    s = s.replace(/(^|[^_\w])_([^_\n]+)_(?![\w_])/g, '$1<em>$2</em>');

    return s.replace(/\u0000(\d+)\u0001/g, (_m, n: string) => stash[Number(n)] ?? '');
}

// ---- Blocks ----------------------------------------------------------------

const FENCE_RE = /^\s{0,3}(```+|~~~+)\s*([\w+#.-]*)\s*$/;
const HEADING_RE = /^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$/;
const HR_RE = /^\s{0,3}([-*_])\s*(\1\s*){2,}$/;
const QUOTE_RE = /^\s{0,3}>\s?/;
const LIST_RE = /^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$/;
const TABLE_SEP_RE = /^\s{0,3}\|?[\s:|-]*-{2,}[\s:|-]*\|?\s*$/;

interface ListItem {
    indent: number;
    ordered: boolean;
    lines: string[];
}

function indentWidth(s: string): number {
    return s.replace(/\t/g, '    ').length;
}

function isTableAt(lines: string[], i: number): boolean {
    const head = lines[i] ?? '';
    const sep = lines[i + 1] ?? '';
    return head.includes('|') && sep.includes('|') && sep.includes('-') && TABLE_SEP_RE.test(sep);
}

function startsBlock(lines: string[], i: number): boolean {
    const line = lines[i];
    return (
        FENCE_RE.test(line) ||
        HEADING_RE.test(line) ||
        HR_RE.test(line) ||
        QUOTE_RE.test(line) ||
        LIST_RE.test(line) ||
        isTableAt(lines, i)
    );
}

function splitRow(row: string): string[] {
    return row
        .trim()
        .replace(/^\|/, '')
        .replace(/\|$/, '')
        .split('|')
        .map((c) => c.trim());
}

function alignments(sep: string): string[] {
    return splitRow(sep).map((c) => {
        const left = c.startsWith(':');
        const right = c.endsWith(':');
        if (left && right) return 'center';
        if (right) return 'right';
        if (left) return 'left';
        return '';
    });
}

function cellStyle(align: string | undefined): string {
    return align ? ` style="text-align:${align}"` : '';
}

function renderTable(lines: string[], start: number): { html: string; next: number } {
    const align = alignments(lines[start + 1]);
    const head = splitRow(lines[start]);
    let i = start + 2;
    const rows: string[][] = [];
    while (i < lines.length && lines[i].includes('|') && lines[i].trim()) {
        rows.push(splitRow(lines[i]));
        i++;
    }
    const th = head
        .map((c, n) => `<th${cellStyle(align[n])}>${renderInline(c)}</th>`)
        .join('');
    const body = rows
        .map(
            (r) =>
                '<tr>' +
                r.map((c, n) => `<td${cellStyle(align[n])}>${renderInline(c)}</td>`).join('') +
                '</tr>',
        )
        .join('');
    return {
        html: `<table><thead><tr>${th}</tr></thead><tbody>${body}</tbody></table>`,
        next: i,
    };
}

function renderItem(item: ListItem): string {
    const first = item.lines[0] ?? '';
    const task = /^\[([ xX])\]\s+(.*)$/.exec(first);
    const body = task ? [task[2], ...item.lines.slice(1)] : item.lines;
    const box = task ? (task[1].toLowerCase() === 'x' ? '☑ ' : '☐ ') : '';
    return box + body.map((l) => renderInline(l)).join('<br />');
}

function buildList(items: ListItem[], pos: number): { html: string; next: number } {
    const indent = items[pos].indent;
    const ordered = items[pos].ordered;
    const tag = ordered ? 'ol' : 'ul';
    let html = `<${tag}>`;
    let i = pos;
    while (i < items.length && items[i].indent >= indent) {
        if (items[i].indent > indent || items[i].ordered !== ordered) break;
        html += `<li>${renderItem(items[i])}`;
        i++;
        // Deeper items belong inside the <li> we have just opened.
        while (i < items.length && items[i].indent > indent) {
            const sub = buildList(items, i);
            html += sub.html;
            i = sub.next;
        }
        html += '</li>';
    }
    return { html: html + `</${tag}>`, next: i };
}

function collectList(lines: string[], start: number): { items: ListItem[]; next: number } {
    const items: ListItem[] = [];
    let i = start;
    while (i < lines.length) {
        const m = LIST_RE.exec(lines[i]);
        if (m) {
            items.push({
                indent: indentWidth(m[1]),
                ordered: /\d/.test(m[2]),
                lines: [m[3]],
            });
            i++;
            continue;
        }
        // A blank line only ends the list if no further item follows (loose list).
        if (!lines[i].trim()) {
            if (i + 1 < lines.length && LIST_RE.test(lines[i + 1])) {
                i++;
                continue;
            }
            break;
        }
        // Indented continuation of the previous item.
        if (items.length && /^\s{2,}\S/.test(lines[i])) {
            items[items.length - 1].lines.push(lines[i].trim());
            i++;
            continue;
        }
        break;
    }
    return { items, next: i };
}

function renderBlocks(lines: string[]): string {
    const out: string[] = [];
    let i = 0;
    while (i < lines.length) {
        const line = lines[i];
        if (!line.trim()) {
            i++;
            continue;
        }

        const fence = FENCE_RE.exec(line);
        if (fence) {
            const closer = fence[1][0] === '`' ? /^\s{0,3}```+\s*$/ : /^\s{0,3}~~~+\s*$/;
            const body: string[] = [];
            i++;
            while (i < lines.length && !closer.test(lines[i])) {
                body.push(lines[i]);
                i++;
            }
            if (i < lines.length) i++; // closing fence
            const lang = fence[2] ? ` class="lang-${escapeHtml(fence[2])}"` : '';
            out.push(`<pre><code${lang}>${escapeHtml(body.join('\n'))}</code></pre>`);
            continue;
        }

        if (HR_RE.test(line)) {
            out.push('<hr />');
            i++;
            continue;
        }

        const heading = HEADING_RE.exec(line);
        if (heading) {
            const level = heading[1].length;
            out.push(`<h${level}>${renderInline(heading[2])}</h${level}>`);
            i++;
            continue;
        }

        if (QUOTE_RE.test(line)) {
            const body: string[] = [];
            while (i < lines.length && QUOTE_RE.test(lines[i])) {
                body.push(lines[i].replace(QUOTE_RE, ''));
                i++;
            }
            out.push(`<blockquote>${renderBlocks(body)}</blockquote>`);
            continue;
        }

        if (isTableAt(lines, i)) {
            const t = renderTable(lines, i);
            out.push(t.html);
            i = t.next;
            continue;
        }

        if (LIST_RE.test(line)) {
            const { items, next } = collectList(lines, i);
            let p = 0;
            while (p < items.length) {
                const built = buildList(items, p);
                out.push(built.html);
                p = built.next > p ? built.next : p + 1;
            }
            i = next;
            continue;
        }

        // Paragraph: consecutive lines until a blank line or another block.
        const para: string[] = [];
        while (i < lines.length && lines[i].trim() && !startsBlock(lines, i)) {
            para.push(lines[i].trim());
            i++;
        }
        if (para.length === 0) {
            // Defensive: every block branch above must consume its own line.
            para.push(lines[i]);
            i++;
        }
        out.push(`<p>${para.map((l) => renderInline(l)).join('<br />')}</p>`);
    }
    return out.join('');
}

/** Render markdown source to safe HTML. All input is escaped. */
export function renderMarkdown(src: string | undefined | null): string {
    const text = String(src ?? '').replace(/\r\n?/g, '\n');
    if (!text.trim()) return '';
    return renderBlocks(text.split('\n'));
}
