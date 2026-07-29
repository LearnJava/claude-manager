/**
 * Unit spec for the log's markdown renderer (src/lib/markdown.ts).
 *
 * Pure functions, no browser needed — these run in the Playwright runner's
 * Node context. The DOM side (the Markdown checkbox in LogStream) is covered
 * by log-markdown.spec.ts.
 *
 * The renderer's output is injected with {@html}, so the escaping cases below
 * are load-bearing, not cosmetic: anything that survives unescaped becomes
 * executable markup in the app window.
 */

import { test, expect } from '@playwright/test';
import {
    escapeHtml,
    hasMarkdown,
    hasStrongMarkdown,
    looksLikeMachineOutput,
    renderInline,
    renderMarkdown,
} from '../src/lib/markdown';

test.describe('escapeHtml', () => {
    test('escapes every HTML-significant character', () => {
        expect(escapeHtml(`<a href="x" onclick='y'>&</a>`)).toBe(
            '&lt;a href=&quot;x&quot; onclick=&#39;y&#39;&gt;&amp;&lt;/a&gt;',
        );
    });

    test('tolerates null and undefined', () => {
        expect(escapeHtml(undefined as unknown as string)).toBe('');
        expect(escapeHtml(null as unknown as string)).toBe('');
    });
});

test.describe('hasMarkdown', () => {
    const positives: Array<[string, string]> = [
        ['heading', '# Title'],
        ['sub heading', 'text\n\n### Section\nbody'],
        ['bullet list', '- one\n- two'],
        ['ordered list', '1. one\n2. two'],
        ['fenced code', '```go\nfmt.Println()\n```'],
        ['inline code', 'run `go test ./...` now'],
        ['bold', 'this is **important**'],
        ['italic', 'this is _important_'],
        ['strikethrough', 'this is ~~gone~~'],
        ['blockquote', '> quoted'],
        ['table', '| a | b |\n|---|---|\n| 1 | 2 |'],
        ['horizontal rule', 'a\n\n---\n\nb'],
        ['link', 'see [docs](https://example.com)'],
    ];
    for (const [name, src] of positives) {
        test(`detects ${name}`, () => {
            expect(hasMarkdown(src)).toBe(true);
        });
    }

    const negatives: Array<[string, string]> = [
        ['plain prose', 'Working on the task...'],
        ['a bare path', 'reading D:/GoProjects/claude-manager/app.go'],
        ['a shell line', '$ go build ./... && echo done'],
        ['empty', ''],
        ['whitespace only', '   \n  '],
    ];
    for (const [name, src] of negatives) {
        test(`ignores ${name}`, () => {
            expect(hasMarkdown(src)).toBe(false);
        });
    }

    test('tolerates null and undefined', () => {
        expect(hasMarkdown(undefined)).toBe(false);
        expect(hasMarkdown(null)).toBe(false);
    });
});

test.describe('hasStrongMarkdown', () => {
    const structural: Array<[string, string]> = [
        ['a table', '| a | b |\n|---|---|\n| 1 | 2 |'],
        ['a fence', '```go\nx := 1\n```'],
        ['a heading', '## Report\n\nbody'],
    ];
    for (const [name, src] of structural) {
        test(`accepts ${name} on its own`, () => {
            expect(hasStrongMarkdown(src)).toBe(true);
        });
    }

    test('accepts two different kinds of markup together', () => {
        expect(
            hasStrongMarkdown('- **Invariant (BUG-341):** `chrome_layout` is read-only'),
        ).toBe(true);
    });

    test('rejects a single stray signal', () => {
        expect(hasStrongMarkdown('- fixed the thing\n- and another')).toBe(false);
        expect(hasStrongMarkdown('run `cargo build` first')).toBe(false);
        expect(hasStrongMarkdown('')).toBe(false);
        expect(hasStrongMarkdown(null)).toBe(false);
    });
});

test.describe('looksLikeMachineOutput', () => {
    const positives: Array<[string, string]> = [
        ['Read line numbers', '1\t    Checking simd-adler32 v0.3.9\n2\t   Compiling syn v2.0.117'],
        [
            'a tool wrapper tag',
            '<persisted-output>\nOutput too large (116.5KB).\n\nPreview:\n| a | b |',
        ],
        ['a diff', 'diff --git a/ROADMAP.md b/ROADMAP.md\nindex f1ecad92..fb50ef8b 100644'],
        ['a hunk header', '@@ -106,7 +106,7 @@ context\n line'],
        ['a git listing', '  remotes/origin/p1-history-nav-api\n  remotes/origin/p1-meta-viewport'],
        [
            'mostly indented columns',
            '    Checking a v1\n    Checking b v2\n    Checking c v3\n    Checking d v4\n' +
                '    Checking e v5\ntail',
        ],
    ];
    for (const [name, src] of positives) {
        test(`detects ${name}`, () => {
            expect(looksLikeMachineOutput(src)).toBe(true);
        });
    }

    test('leaves prose and documents alone', () => {
        expect(looksLikeMachineOutput('## Report\n\nSome **bold** prose.')).toBe(false);
        expect(looksLikeMachineOutput('- one\n- two')).toBe(false);
        expect(looksLikeMachineOutput('')).toBe(false);
        expect(looksLikeMachineOutput(undefined)).toBe(false);
    });

    test('a short indented snippet is not output', () => {
        // Fewer than 5 lines: not enough evidence to call it a column listing.
        expect(looksLikeMachineOutput('    a\n    b')).toBe(false);
    });
});

test.describe('renderInline', () => {
    test('renders bold, italic and strikethrough', () => {
        expect(renderInline('**b** and _i_ and ~~s~~')).toBe(
            '<strong>b</strong> and <em>i</em> and <del>s</del>',
        );
    });

    test('bold wins over italic for **x**', () => {
        expect(renderInline('**x**')).toBe('<strong>x</strong>');
    });

    test('code spans are escaped and never re-processed', () => {
        expect(renderInline('use `<b>**raw**</b>` here')).toBe(
            'use <code>&lt;b&gt;**raw**&lt;/b&gt;</code> here',
        );
    });

    test('renders a link and keeps emphasis in its label', () => {
        const html = renderInline('see [**docs**](https://example.com/a?x=1&y=2)');
        expect(html).toContain('<a href="https://example.com/a?x=1&amp;y=2"');
        expect(html).toContain('target="_blank"');
        expect(html).toContain('rel="noreferrer noopener"');
        expect(html).toContain('<strong>docs</strong>');
    });

    test('drops unsafe link schemes but keeps the label', () => {
        const html = renderInline('[click](javascript:alert(1))');
        expect(html).not.toContain('<a ');
        expect(html).not.toContain('javascript:');
        expect(html).toContain('click');
    });

    test('autolinks bare URLs', () => {
        const html = renderInline('open https://example.com/x now');
        expect(html).toContain('<a href="https://example.com/x"');
        expect(html).toContain('>https://example.com/x</a>');
    });

    test('escapes raw HTML', () => {
        expect(renderInline('<img src=x onerror=alert(1)>')).toBe(
            '&lt;img src=x onerror=alert(1)&gt;',
        );
    });
});

test.describe('renderMarkdown blocks', () => {
    test('headings by level', () => {
        expect(renderMarkdown('# One\n## Two\n###### Six')).toBe(
            '<h1>One</h1><h2>Two</h2><h6>Six</h6>',
        );
    });

    test('paragraph keeps soft line breaks', () => {
        expect(renderMarkdown('line one\nline two')).toBe(
            '<p>line one<br />line two</p>',
        );
    });

    test('blank line separates paragraphs', () => {
        expect(renderMarkdown('a\n\nb')).toBe('<p>a</p><p>b</p>');
    });

    test('unordered list', () => {
        expect(renderMarkdown('- one\n- two')).toBe(
            '<ul><li>one</li><li>two</li></ul>',
        );
    });

    test('ordered list', () => {
        expect(renderMarkdown('1. one\n2. two')).toBe(
            '<ol><li>one</li><li>two</li></ol>',
        );
    });

    test('nested list is placed inside its parent item', () => {
        expect(renderMarkdown('- top\n    - child\n- next')).toBe(
            '<ul><li>top<ul><li>child</li></ul></li><li>next</li></ul>',
        );
    });

    test('task list items get a checkbox glyph', () => {
        const html = renderMarkdown('- [x] done\n- [ ] open');
        expect(html).toBe('<ul><li>☑ done</li><li>☐ open</li></ul>');
    });

    test('fenced code block keeps its content verbatim and escaped', () => {
        const html = renderMarkdown('```go\nif a < b && c > d {}\n```');
        expect(html).toBe(
            '<pre><code class="lang-go">if a &lt; b &amp;&amp; c &gt; d {}</code></pre>',
        );
    });

    test('markdown inside a fenced block is not rendered', () => {
        expect(renderMarkdown('```\n# not a heading\n- not a list\n```')).toBe(
            '<pre><code># not a heading\n- not a list</code></pre>',
        );
    });

    test('unterminated fence still renders as code', () => {
        expect(renderMarkdown('```\nabc')).toBe('<pre><code>abc</code></pre>');
    });

    test('blockquote renders nested blocks', () => {
        expect(renderMarkdown('> quoted **text**')).toBe(
            '<blockquote><p>quoted <strong>text</strong></p></blockquote>',
        );
    });

    test('horizontal rule', () => {
        expect(renderMarkdown('a\n\n---\n\nb')).toBe('<p>a</p><hr /><p>b</p>');
    });

    test('table with alignment', () => {
        const html = renderMarkdown('| Step | Lumen |\n|---|:---:|\n| type | ok |');
        expect(html).toBe(
            '<table><thead><tr><th>Step</th><th style="text-align:center">Lumen</th></tr></thead>' +
                '<tbody><tr><td>type</td><td style="text-align:center">ok</td></tr></tbody></table>',
        );
    });

    test('table keeps rows that follow a blank-line-free run', () => {
        const html = renderMarkdown('| a |\n|---|\n| 1 |\n| 2 |');
        expect(html).toContain('<td>1</td>');
        expect(html).toContain('<td>2</td>');
    });

    test('mixed document keeps block order', () => {
        const html = renderMarkdown(
            '## Report\n\nSome **bold** prose.\n\n- first\n- second\n\n```sh\ngo test\n```',
        );
        expect(html).toBe(
            '<h2>Report</h2><p>Some <strong>bold</strong> prose.</p>' +
                '<ul><li>first</li><li>second</li></ul>' +
                '<pre><code class="lang-sh">go test</code></pre>',
        );
    });

    test('CRLF input is normalized', () => {
        expect(renderMarkdown('# A\r\n\r\nb')).toBe('<h1>A</h1><p>b</p>');
    });

    test('empty input renders nothing', () => {
        expect(renderMarkdown('')).toBe('');
        expect(renderMarkdown('   \n  ')).toBe('');
        expect(renderMarkdown(undefined)).toBe('');
    });

    test('script tags never survive', () => {
        const html = renderMarkdown('# <script>alert(1)</script>\n\n<img onerror=x>');
        expect(html).not.toContain('<script');
        expect(html).not.toContain('<img');
        expect(html).toContain('&lt;script&gt;');
    });
});
