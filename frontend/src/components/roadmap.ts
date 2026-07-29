// Shape of the roadmap tree returned by GetSessionRoadmap
// (internal/analysis/roadmapview.go). Kept hand-written rather than imported
// from wailsjs/go/models because the Go type is recursive and the generated
// model flattens Children into `any`.

export type RoadmapStatus = 'done' | 'active' | 'blocked' | 'pending';

export type RoadmapNode = {
    kind: 'phase' | 'task';
    id: string;
    number: number;
    name: string;
    summary: string;
    status: RoadmapStatus;
    /** As written in the roadmap's own status column: planned, opt, ready… */
    status_raw: string;
    /** The first pointer in the session's status file — what it works on next. */
    current: boolean;
    /** Any pointer in the session's status file. */
    in_queue: boolean;
    detail_path: string;
    depends_on: string;
    bugs: string;
    size: string;
    line: number;
    /** File this node was read from — a status file may span several roadmaps. */
    roadmap_file: string;
    has_detail: boolean;
    children?: RoadmapNode[];
    done: number;
    total: number;
};

export type RoadmapView = {
    title: string;
    roadmap_file: string;
    status_file: string;
    context: string;
    /**
     * "pointer" (done = no pointer), "curated" (the roadmap's status column),
     * or "mixed" when the queue spans roadmaps that disagree.
     */
    status_model: 'pointer' | 'curated' | 'mixed';
    nodes: RoadmapNode[];
    done: number;
    total: number;
};

/** True when node or any descendant is the session's current task. */
export function containsCurrent(node: RoadmapNode): boolean {
    if (node.current) return true;
    return (node.children ?? []).some(containsCurrent);
}
