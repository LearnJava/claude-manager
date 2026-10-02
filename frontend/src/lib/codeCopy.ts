// Svelte action: puts a header with the fence language and a copy button
// above every <pre> inside a rendered markdown body. Done as DOM decoration
// rather than in renderMarkdown so the markdown renderer stays a pure
// string function with no i18n or event handling.
import { tick } from 'svelte';
import { copyText, COPY_FEEDBACK_MS } from './clipboard';

export interface CodeCopyOptions {
    // The rendered HTML — passed only so the action re-runs when {@html}
    // replaces the content.
    html: string;
    label: string;
    copied: string;
    failed: string;
}

const HEAD_CLASS = 'code-head';

// Fence language from the class renderMarkdown puts on <code> (`lang-go`).
export function codeLang(code: Element | null): string {
    const m = /(?:^|\s)lang-(\S+)/.exec(code?.className ?? '');
    return m ? m[1] : '';
}

export function codeCopy(node: HTMLElement, opts: CodeCopyOptions) {
    let current = opts;
    const timers = new Set<ReturnType<typeof setTimeout>>();

    function decorate() {
        // {@html} detaches only the nodes it inserted, so headers from a
        // previous render can be left behind — drop them all and rebuild.
        node.querySelectorAll(`.${HEAD_CLASS}`).forEach((h) => h.remove());
        node.querySelectorAll('pre').forEach((pre) => {
            const code = pre.querySelector('code');
            const head = document.createElement('div');
            head.className = HEAD_CLASS;
            const lang = document.createElement('span');
            lang.textContent = codeLang(code);
            const btn = document.createElement('button');
            btn.type = 'button';
            btn.className = 'code-copy';
            btn.textContent = current.label;
            btn.addEventListener('click', async () => {
                const ok = await copyText((code ?? pre).textContent ?? '');
                btn.textContent = ok ? current.copied : current.failed;
                const t = setTimeout(() => {
                    timers.delete(t);
                    btn.textContent = current.label;
                }, COPY_FEEDBACK_MS);
                timers.add(t);
            });
            head.append(lang, btn);
            pre.before(head);
        });
    }

    decorate();
    return {
        update(next: CodeCopyOptions) {
            current = next;
            // Let {@html} swap its nodes in first.
            tick().then(decorate);
        },
        destroy() {
            timers.forEach(clearTimeout);
        },
    };
}
