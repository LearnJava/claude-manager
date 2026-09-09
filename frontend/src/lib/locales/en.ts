// Aggregates every per-component English fragment into one flat dictionary.
// Values here MUST stay byte-identical to the original hardcoded English
// strings they replace — Playwright specs assert on this text, and the app
// defaults to English precisely so that stays true (see i18n.ts).
import common from './en/common';
import notifications from './en/notifications';
import app from './en/app';
import sidebar from './en/sidebar';
import sessionCard from './en/sessionCard';
import sessionView from './en/sessionView';
import sessionInput from './en/sessionInput';
import statusBar from './en/statusBar';
import taskPanel from './en/taskPanel';
import permissionBanner from './en/permissionBanner';
import permissionQueue from './en/permissionQueue';
import questionBanner from './en/questionBanner';
import resumePrompt from './en/resumePrompt';
import modelPicker from './en/modelPicker';
import logStream from './en/logStream';
import roadmapTree from './en/roadmapTree';
import roadmapNode from './en/roadmapNode';
import history from './en/history';
import costDashboard from './en/costDashboard';
import planReview from './en/planReview';
import skillReview from './en/skillReview';
import experiencePanel from './en/experiencePanel';
import mixedRun from './en/mixedRun';
import settings from './en/settings';
import alienCrew from './en/alienCrew';

export default {
    ...common,
    ...notifications,
    ...app,
    ...sidebar,
    ...sessionCard,
    ...sessionView,
    ...sessionInput,
    ...statusBar,
    ...taskPanel,
    ...permissionBanner,
    ...permissionQueue,
    ...questionBanner,
    ...resumePrompt,
    ...modelPicker,
    ...logStream,
    ...roadmapTree,
    ...roadmapNode,
    ...history,
    ...costDashboard,
    ...planReview,
    ...skillReview,
    ...experiencePanel,
    ...mixedRun,
    ...settings,
    ...alienCrew,
} as Record<string, string>;
