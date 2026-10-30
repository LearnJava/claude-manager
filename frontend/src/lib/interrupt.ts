import type { SessionState } from '../stores/sessions';
import { capabilitiesOf } from './models';

// canInterrupt: the session has an action in flight that InterruptSession can
// abort — a Claude CLI turn (a per-turn runtime starts a process per turn and
// has no control channel), either running or held at a permission prompt.
// Activity is unset until the first stream event of a run, which is still a turn.
export function canInterrupt(s: SessionState | undefined): boolean {
    if (!s || capabilitiesOf(s.runtime).perTurnProcess) return false;
    if (s.status === 'waiting_permission') return true;
    return s.status === 'working' && s.activity?.kind !== 'idle';
}
