// Aggregates every per-component Russian fragment into one flat dictionary.
// Same keys as en.ts — a key present in one locale must be present in the
// other (English is the fallback for anything missing here, see i18n.ts).
import common from './ru/common';
import notifications from './ru/notifications';
import app from './ru/app';
import sidebar from './ru/sidebar';
import sessionCard from './ru/sessionCard';
import sessionView from './ru/sessionView';
import sessionInput from './ru/sessionInput';
import statusBar from './ru/statusBar';
import taskPanel from './ru/taskPanel';
import permissionBanner from './ru/permissionBanner';
import permissionQueue from './ru/permissionQueue';
import questionBanner from './ru/questionBanner';
import resumePrompt from './ru/resumePrompt';
import modelPicker from './ru/modelPicker';
import logStream from './ru/logStream';
import roadmapTree from './ru/roadmapTree';
import roadmapNode from './ru/roadmapNode';
import history from './ru/history';
import costDashboard from './ru/costDashboard';
import planReview from './ru/planReview';
import skillReview from './ru/skillReview';
import experiencePanel from './ru/experiencePanel';
import mixedRun from './ru/mixedRun';
import settings from './ru/settings';
import alienCrew from './ru/alienCrew';

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
