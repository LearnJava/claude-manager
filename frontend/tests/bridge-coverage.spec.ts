import { test, expect } from '@playwright/test';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));

// Browser-free guard: every App method the frontend imports from
// wailsjs/go/main/App must have a stub in the Playwright bridge, otherwise the
// panel using it shows "App.<Name> is not a function" in GUI tests.

const SRC = join(HERE, '..', 'src');
const BRIDGE = join(HERE, 'helpers', 'bridge.ts');

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|js|svelte)$/.test(e)) out.push(p);
  }
  return out;
}

function importedAppMethods(): Set<string> {
  const used = new Set<string>();
  const re = /import\s*\{([^}]*)\}\s*from\s*'[^']*wailsjs\/go\/main\/App'/g;
  for (const f of walk(SRC)) {
    const text = readFileSync(f, 'utf-8');
    for (const m of text.matchAll(re)) {
      for (const part of m[1].split(',')) {
        const name = part.trim().split(/\s+as\s+/)[0].trim();
        if (name) used.add(name);
      }
    }
  }
  return used;
}

function bridgeMethods(): Set<string> {
  const text = readFileSync(BRIDGE, 'utf-8');
  const start = text.indexOf('const App = {');
  expect(start, 'App stub block not found in bridge.ts').toBeGreaterThan(-1);
  const keys = new Set<string>();
  for (const m of text.slice(start).matchAll(/^ {8}(\w+)\s*[:(]/gm)) keys.add(m[1]);
  return keys;
}

test('bridge stubs every App method the frontend imports', () => {
  const stubbed = bridgeMethods();
  const missing = [...importedAppMethods()].filter((n) => !stubbed.has(n)).sort();
  expect(missing, `bridge.ts lacks stubs for: ${missing.join(', ')}`).toEqual([]);
});
