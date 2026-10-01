/**
 * Unit spec for the queue model (src/lib/queue.ts): the slice of a roadmap tree
 * a session's status file points at, as shown by the Queue tab, the sidebar
 * mini-bar and the board.
 */

import { test, expect } from '@playwright/test';
import {
    buildQueue,
    parseDeps,
    queueSegments,
    queuedCount,
    hasQueue,
    sizeChipClass,
    DONE_RECENT_LIMIT,
    SEGMENT_LIMIT,
    EMPTY_QUEUE,
} from '../src/lib/queue';
import type { RoadmapNode, RoadmapView } from '../src/components/roadmap';

function task(id: string, o: Partial<RoadmapNode> = {}): RoadmapNode {
    return {
        kind: 'task', id, number: 0, name: `task ${id}`, summary: '', status: 'pending',
        status_raw: '', current: false, in_queue: false, detail_path: '', depends_on: '',
        bugs: '', size: '', line: 1, roadmap_file: 'ROADMAP.md', has_detail: false,
        done: 0, total: 1, ...o,
    };
}

function view(nodes: RoadmapNode[], done = 0): RoadmapView {
    return {
        title: 'R', roadmap_file: 'ROADMAP.md', status_file: 'STATUS-P1.md', context: '',
        status_model: 'curated', nodes, done, total: nodes.length,
    };
}

test.describe('buildQueue', () => {
    test('null / empty view gives the empty queue', () => {
        expect(buildQueue(null)).toBe(EMPTY_QUEUE);
        expect(buildQueue(undefined)).toBe(EMPTY_QUEUE);
        expect(hasQueue(buildQueue(view([])))).toBe(false);
    });

    test('current row is lifted out, the rest keep roadmap order', () => {
        const m = buildQueue(view([
            task('A', { in_queue: true }),
            task('B', { in_queue: true, current: true }),
            task('C', { in_queue: true }),
            task('D'), // not queued
        ]));
        expect(m.current?.id).toBe('B');
        expect(m.upcoming.map((i) => i.id)).toEqual(['A', 'C']);
        expect(queuedCount(m)).toBe(3);
        expect(hasQueue(m)).toBe(true);
    });

    test('walks into phases and nested tasks', () => {
        const phase: RoadmapNode = { ...task('P1'), kind: 'phase', children: [
            task('T1', { in_queue: true, current: true }),
            { ...task('T2', { in_queue: true }), children: [task('T2a', { in_queue: true })] },
        ] };
        const m = buildQueue(view([phase]));
        expect(m.current?.id).toBe('T1');
        expect(m.upcoming.map((i) => i.id)).toEqual(['T2', 'T2a']);
    });

    test('a pointer left on a done row is not queued', () => {
        const m = buildQueue(view([task('A', { in_queue: true, status: 'done' })]));
        expect(hasQueue(m)).toBe(false);
    });

    test('waitsFor lists only dependencies that are themselves still queued', () => {
        const m = buildQueue(view([
            task('RT-04', { in_queue: true, current: true }),
            task('RT-05', { in_queue: true, depends_on: 'RT-04' }),
            task('RT-06', { in_queue: true, depends_on: 'rt-05, RT-01' }), // RT-01 not queued
            task('RT-07', { in_queue: true, depends_on: '-' }),
            task('RT-08', { in_queue: true, depends_on: 'RT-08' }), // self is ignored
        ]));
        const by = Object.fromEntries(m.upcoming.map((i) => [i.id, i.waitsFor]));
        expect(by['RT-05']).toEqual(['RT-04']);
        expect(by['RT-06']).toEqual(['rt-05']);
        expect(by['RT-07']).toEqual([]);
        expect(by['RT-08']).toEqual([]);
    });

    test('generated roadmaps without ids fall back to #number', () => {
        const m = buildQueue(view([task('', { number: 7, in_queue: true, current: true, depends_on: '#7' })]));
        expect(m.current?.id).toBe('#7');
    });

    test('done: only leaf tasks, newest first, capped; doneCount comes from the view', () => {
        const many = Array.from({ length: DONE_RECENT_LIMIT + 3 }, (_, i) => task(`D${i}`, { status: 'done' }));
        const parent: RoadmapNode = { ...task('PAR', { status: 'done' }), children: [task('LEAF', { status: 'done' })] };
        const m = buildQueue(view([...many, parent], 99));
        expect(m.doneRecent).toHaveLength(DONE_RECENT_LIMIT);
        expect(m.doneRecent[0].id).toBe('LEAF'); // last in tree order
        expect(m.doneRecent.some((i) => i.id === 'PAR')).toBe(false);
        expect(m.doneCount).toBe(99);
    });

    test('size is trimmed and upper-cased; summary only shown next to a name', () => {
        const m = buildQueue(view([task('A', { in_queue: true, current: true, size: ' m ', name: 'N', summary: 'S' })]));
        expect(m.current?.size).toBe('M');
        expect(m.current?.name).toBe('N');
        expect(m.current?.summary).toBe('S');
        const m2 = buildQueue(view([task('B', { in_queue: true, current: true, name: '', summary: 'only summary' })]));
        expect(m2.current?.name).toBe('only summary');
        expect(m2.current?.summary).toBe('');
    });
});

test.describe('parseDeps', () => {
    test('splits on commas / spaces / semicolons and drops dashes', () => {
        expect(parseDeps('RT-04, RT-05;RT-06 RT-07')).toEqual(['RT-04', 'RT-05', 'RT-06', 'RT-07']);
        expect(parseDeps('-')).toEqual([]);
        expect(parseDeps('—')).toEqual([]);
        expect(parseDeps('')).toEqual([]);
        expect(parseDeps(undefined as unknown as string)).toEqual([]);
    });
});

test.describe('queueSegments', () => {
    const m = buildQueue(view([
        task('A', { in_queue: true, current: true }),
        task('B', { in_queue: true }),
        task('C', { in_queue: true }),
    ]));

    test('done, then current, then queued', () => {
        expect(queueSegments(m, 2)).toEqual(['done', 'done', 'current', 'queued', 'queued']);
    });

    test('no current row: only done and queued', () => {
        const noCurrent = { ...m, current: null };
        expect(queueSegments(noCurrent, 0)).toEqual(['queued', 'queued']);
    });

    test('negative tasksDone is treated as zero; big counts are capped', () => {
        expect(queueSegments(m, -3)[0]).toBe('current');
        const segs = queueSegments(m, 500);
        expect(segs.filter((s) => s === 'done')).toHaveLength(SEGMENT_LIMIT);
    });
});

test.describe('sizeChipClass', () => {
    test('known sizes get a light + dark tint, unknown stays neutral', () => {
        for (const s of ['S', 'M', 'L', 'XL']) {
            const c = sizeChipClass(s);
            expect(c).toMatch(/dark:/);
        }
        expect(sizeChipClass('')).toBe('text-text-muted');
        expect(sizeChipClass('?')).toBe('text-text-muted');
    });
});
