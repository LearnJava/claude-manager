<script lang="ts">
    import { sessionList, sessionLogs } from '../stores/sessions';
    import { derived } from 'svelte/store';

    type Activity =
        | 'idle' | 'reading' | 'writing' | 'searching'
        | 'executing' | 'analyzing' | 'waiting' | 'sleeping'
        | 'error' | 'starting';

    function toolToActivity(name: string): Activity {
        const t = (name ?? '').toLowerCase();
        if (t.includes('read')) return 'reading';
        if (t.includes('write') || t.includes('edit')) return 'writing';
        if (t.includes('grep') || t.includes('glob') || t.includes('search') || t.includes('find')) return 'searching';
        if (t.includes('bash') || t.includes('execute') || t.includes('shell')) return 'executing';
        if (t.includes('web') || t.includes('fetch')) return 'analyzing';
        if (t.includes('agent') || t.includes('task')) return 'analyzing';
        return 'executing';
    }

    function toolCaption(name: string): string {
        const t = (name ?? '').toLowerCase();
        if (t.includes('read'))   return 'Reading the charts!';
        if (t.includes('write'))  return 'Rewriting the log!';
        if (t.includes('edit'))   return 'Patching the hull!';
        if (t.includes('glob'))   return 'Scanning the map!';
        if (t.includes('grep') || t.includes('search')) return 'Spotted something!';
        if (t.includes('find'))   return 'Searching the hold!';
        if (t.includes('bash') || t.includes('shell'))  return 'Hard to port!';
        if (t.includes('web') || t.includes('fetch'))   return 'Signal incoming!';
        if (t.includes('agent') || t.includes('task'))  return 'Deploy the crew!';
        return 'Working the sails!';
    }

    const scene = derived([sessionList, sessionLogs], ([sessions, logs]) => {
        if (sessions.some(s => s.status === 'error'))
            return { activity: 'error' as Activity, label: 'All hands on deck!' };
        if (sessions.some(s => s.status === 'waiting_permission' || s.status === 'waiting_for_user'))
            return { activity: 'waiting' as Activity, label: 'Awaiting orders...' };
        if (sessions.some(s => s.status === 'rate_limited' || s.status === 'retrying'))
            return { activity: 'sleeping' as Activity, label: 'Off watch...' };
        const working = sessions.find(s => s.status === 'working');
        if (working) {
            const sl = logs[working.id] ?? [];
            for (let i = sl.length - 1; i >= 0; i--) {
                if (sl[i].tool_name) {
                    return { activity: toolToActivity(sl[i].tool_name!), label: toolCaption(sl[i].tool_name!) };
                }
            }
            return { activity: 'executing' as Activity, label: 'Working the sails!' };
        }
        if (sessions.some(s => s.status === 'starting' || s.status === 'analyzing'))
            return { activity: 'starting' as Activity, label: 'Raise the sails!' };
        return { activity: 'idle' as Activity, label: 'Sailing the Etherium...' };
    });

    // ── Demo: cycle through all animation states ──
    const DEMO_STEPS: { activity: Activity; label: string }[] = [
        { activity: 'idle',      label: 'Sailing the Etherium...' },
        { activity: 'starting',  label: 'Raise the sails!' },
        { activity: 'reading',   label: 'Reading the charts!' },
        { activity: 'writing',   label: 'Rewriting the log!' },
        { activity: 'searching', label: 'Spotted something!' },
        { activity: 'executing', label: 'Hard to port!' },
        { activity: 'analyzing', label: 'Signal incoming!' },
        { activity: 'waiting',   label: 'Awaiting orders...' },
        { activity: 'sleeping',  label: 'Off watch...' },
        { activity: 'error',     label: 'All hands on deck!' },
    ];

    let demoActivity: Activity | null = null;
    let demoLabel: string | null = null;
    let demoRunning = false;

    async function runDemo() {
        if (demoRunning) return;
        demoRunning = true;
        for (const step of DEMO_STEPS) {
            demoActivity = step.activity;
            demoLabel    = step.label;
            await new Promise<void>(r => setTimeout(r, 1800));
        }
        demoActivity = null;
        demoLabel    = null;
        demoRunning  = false;
    }

    $: act     = demoActivity ?? $scene.activity;
    $: label   = demoLabel   ?? $scene.label;
    $: isError = act === 'error';
</script>

<div class="tp-panel">
    <div class="tp-title">
        <span>⚓ CREW STATUS LOG ⚓</span>
        <button
            class="tp-demo-btn"
            class:tp-demo-running={demoRunning}
            on:click={runDemo}
            disabled={demoRunning}
            title="Test all animations">
            {demoRunning ? '◼' : '▶'}
        </button>
    </div>
    <div class="tp-body">
        <svg viewBox="0 0 240 114" xmlns="http://www.w3.org/2000/svg" style="width:100%;display:block">
            <defs>
                <linearGradient id="tpSpace" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%"   stop-color="#080618"/>
                    <stop offset="65%"  stop-color="#1a0d32"/>
                    <stop offset="100%" stop-color="#2e1500"/>
                </linearGradient>
                <radialGradient id="tpNeb1" cx="72%" cy="30%" r="42%">
                    <stop offset="0%"   stop-color="#3d1060" stop-opacity="0.55"/>
                    <stop offset="100%" stop-color="#080618" stop-opacity="0"/>
                </radialGradient>
                <radialGradient id="tpNeb2" cx="18%" cy="22%" r="32%">
                    <stop offset="0%"   stop-color="#0d2068" stop-opacity="0.45"/>
                    <stop offset="100%" stop-color="#080618" stop-opacity="0"/>
                </radialGradient>
                <radialGradient id="tpAlert" cx="50%" cy="50%" r="55%">
                    <stop offset="0%"   stop-color="#cc0000" stop-opacity="0.2"/>
                    <stop offset="100%" stop-color="#080618" stop-opacity="0"/>
                </radialGradient>
                <linearGradient id="tpWood" x1="0" y1="0" x2="1" y2="0">
                    <stop offset="0%"   stop-color="#4a2008"/>
                    <stop offset="50%"  stop-color="#6b3518"/>
                    <stop offset="100%" stop-color="#4a2008"/>
                </linearGradient>
                <linearGradient id="tpParch" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%"   stop-color="#f8ecc4"/>
                    <stop offset="100%" stop-color="#e4d098"/>
                </linearGradient>
                <linearGradient id="tpSail" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%"   stop-color="#50d8f8" stop-opacity="0.9"/>
                    <stop offset="100%" stop-color="#1060a0" stop-opacity="0.3"/>
                </linearGradient>
            </defs>

            <!-- ── BACKGROUND ── -->
            <rect width="240" height="93" fill="url(#tpSpace)"/>
            <rect width="240" height="93" fill="url(#tpNeb1)"/>
            <rect width="240" height="93" fill="url(#tpNeb2)"/>
            {#if isError}
                <rect width="240" height="93" fill="url(#tpAlert)">
                    <animate attributeName="opacity" values="0.4;1;0.4" dur="0.35s" repeatCount="indefinite"/>
                </rect>
            {/if}

            <!-- Stars -->
            <circle cx="14"  cy="7"  r="1"   fill="white" opacity="0.85"/>
            <circle cx="40"  cy="4"  r="0.7" fill="white" opacity="0.6"/>
            <circle cx="68"  cy="13" r="1.1" fill="white" opacity="0.8"/>
            <circle cx="97"  cy="6"  r="0.8" fill="white" opacity="0.65"/>
            <circle cx="132" cy="10" r="1"   fill="white" opacity="0.9"/>
            <circle cx="160" cy="5"  r="0.7" fill="white" opacity="0.7"/>
            <circle cx="188" cy="14" r="1.1" fill="white" opacity="0.8"/>
            <circle cx="212" cy="7"  r="0.8" fill="white" opacity="0.6"/>
            <circle cx="229" cy="3"  r="0.6" fill="white" opacity="0.5"/>
            <circle cx="23"  cy="21" r="0.5" fill="white" opacity="0.45"/>
            <circle cx="113" cy="19" r="0.6" fill="white" opacity="0.5"/>
            <circle cx="201" cy="23" r="0.5" fill="white" opacity="0.4"/>
            <!-- Twinkling -->
            <circle cx="78"  cy="8"  r="1.2" fill="#c8e8ff" opacity="0.8">
                <animate attributeName="opacity" values="0.8;0.15;0.8" dur="2.4s" repeatCount="indefinite"/>
            </circle>
            <circle cx="168" cy="11" r="1"   fill="#ffe8c0" opacity="0.75">
                <animate attributeName="opacity" values="0.75;0.15;0.75" dur="1.9s" repeatCount="indefinite"/>
            </circle>

            <!-- ── SHIP DECK ── -->
            <rect x="0" y="91" width="240" height="23" fill="url(#tpWood)"/>
            <line x1="0" y1="91" x2="240" y2="91" stroke="#8B5a28" stroke-width="1.5"/>
            <!-- Plank lines -->
            <line x1="0" y1="96"  x2="240" y2="96"  stroke="#3a1808" stroke-width="0.6" opacity="0.55"/>
            <line x1="0" y1="101" x2="240" y2="101" stroke="#3a1808" stroke-width="0.6" opacity="0.55"/>
            <line x1="0" y1="106" x2="240" y2="106" stroke="#3a1808" stroke-width="0.6" opacity="0.55"/>
            <line x1="50"  y1="91" x2="50"  y2="114" stroke="#3a1808" stroke-width="0.5" opacity="0.4"/>
            <line x1="100" y1="91" x2="100" y2="114" stroke="#3a1808" stroke-width="0.5" opacity="0.4"/>
            <line x1="150" y1="91" x2="150" y2="114" stroke="#3a1808" stroke-width="0.5" opacity="0.4"/>
            <line x1="200" y1="91" x2="200" y2="114" stroke="#3a1808" stroke-width="0.5" opacity="0.4"/>
            <!-- Brass rail -->
            <rect x="0" y="88" width="240" height="5" fill="#7a5a10" rx="1"/>
            <line x1="0" y1="90" x2="240" y2="90" stroke="#DAA520" stroke-width="0.8" opacity="0.5"/>
            <circle cx="28"  cy="90" r="1.8" fill="#c8901a"/>
            <circle cx="80"  cy="90" r="1.8" fill="#c8901a"/>
            <circle cx="130" cy="90" r="1.8" fill="#c8901a"/>
            <circle cx="180" cy="90" r="1.8" fill="#c8901a"/>
            <circle cx="220" cy="90" r="1.8" fill="#c8901a"/>

            <!-- ══════════════════════════════════════
                 JIM  (adventurer, x=52, feet y=90)
                 ══════════════════════════════════════ -->
            <g class="c1-{act}">
                <!-- Boots -->
                <rect x="46" y="77" width="8"  height="13" rx="2" fill="#1a0e05"/>
                <rect x="56" y="77" width="8"  height="13" rx="2" fill="#1a0e05"/>
                <rect x="47" y="83" width="5"  height="2"  rx="0.5" fill="#7a5010"/>
                <rect x="57" y="83" width="5"  height="2"  rx="0.5" fill="#7a5010"/>
                <!-- Trousers -->
                <rect x="47" y="68" width="7" height="11" fill="#182038"/>
                <rect x="57" y="68" width="7" height="11" fill="#182038"/>
                <!-- Belt -->
                <rect x="44" y="65" width="24" height="3" fill="#4a2808"/>
                <rect x="51" y="64" width="6"  height="5" rx="1" fill="#c8901a"/>
                <!-- Coat body -->
                <rect x="44" y="46" width="24" height="21" rx="3" fill="#1a2e50"/>
                <!-- Shirt (cream, visible in V) -->
                <polygon points="52,46 60,46 58,62 52,62" fill="#e0d0a0"/>
                <!-- Coat lapels -->
                <polygon points="52,46 44,54 50,58" fill="#142240"/>
                <polygon points="60,46 68,54 62,58" fill="#142240"/>
                <!-- Coat pocket -->
                <rect x="63" y="58" width="4" height="5" rx="1" fill="none" stroke="#142240" stroke-width="0.8"/>
                <!-- Left arm (reaches toward centre) -->
                <rect x="26" y="49" width="18" height="6" rx="3" fill="#c07848"/>
                <ellipse cx="26" cy="52" rx="3.5" ry="4" fill="#c07848"/>
                <!-- Right arm -->
                <rect x="68" y="52" width="15" height="6" rx="3" fill="#1a2e50" transform="rotate(-12,68,55)"/>
                <!-- HAIR (spiky, dark) -->
                <polygon points="40,36 44,22 48,34" fill="#1a0e05"/>
                <polygon points="47,31 52,17 57,31" fill="#1a0e05"/>
                <polygon points="55,33 60,21 65,35" fill="#1a0e05"/>
                <ellipse cx="52" cy="30" rx="13" ry="8" fill="#1a0e05"/>
                <!-- Head (skin) -->
                <ellipse cx="52" cy="37" rx="11" ry="12" fill="#c07848"/>
                <!-- Hair cap over forehead -->
                <ellipse cx="52" cy="27" rx="12" ry="7" fill="#1a0e05"/>
                <!-- Headband -->
                <rect x="40" y="31" width="24" height="3" rx="1" fill="#8B1818"/>
                <!-- Ear -->
                <ellipse cx="41" cy="37" rx="2.5" ry="3"   fill="#b06838"/>
                <ellipse cx="63" cy="37" rx="2.5" ry="3"   fill="#b06838"/>
                <!-- Eyes -->
                {#if act === 'sleeping'}
                    <line x1="47" y1="37" x2="53" y2="37" stroke="#904828" stroke-width="1.8" stroke-linecap="round"/>
                    <line x1="57" y1="37" x2="63" y2="37" stroke="#904828" stroke-width="1.8" stroke-linecap="round"/>
                {:else}
                    <ellipse cx="49" cy="37" rx="3"   ry="2.8" fill="#182818"/>
                    <ellipse cx="59" cy="37" rx="3"   ry="2.8" fill="#182818"/>
                    <circle  cx="48.5" cy="36.2" r="1" fill="white" opacity="0.45"/>
                    <circle  cx="58.5" cy="36.2" r="1" fill="white" opacity="0.45"/>
                {/if}
                <!-- Eyebrows -->
                <line x1="46" y1="32" x2="52" y2="33" stroke="#1a0e05" stroke-width="1.3" stroke-linecap="round"/>
                <line x1="56" y1="33" x2="62" y2="32" stroke="#1a0e05" stroke-width="1.3" stroke-linecap="round"/>
                <!-- Nose -->
                <path d="M51,41 L49,44 L55,44" stroke="#904828" stroke-width="0.9" fill="none" stroke-linecap="round"/>
                <!-- Mouth -->
                {#if act === 'error'}
                    <ellipse cx="52" cy="47" rx="3" ry="2.5" fill="#2d1010"/>
                {:else if act === 'waiting'}
                    <line x1="48" y1="47" x2="56" y2="47" stroke="#904828" stroke-width="1.2" stroke-linecap="round"/>
                {:else if act === 'sleeping'}
                    <path d="M48,47 Q52,51 56,47" stroke="#904828" stroke-width="1.2" fill="none" stroke-linecap="round"/>
                {:else}
                    <path d="M48,46 Q52,50 56,46" stroke="#904828" stroke-width="1.2" fill="none" stroke-linecap="round"/>
                {/if}
            </g>

            <!-- ══════════════════════════════════════
                 PROP / TOOL  (centre, x=120)
                 ══════════════════════════════════════ -->
            {#if act === 'idle' || act === 'executing'}
                <!-- Ship's wheel -->
                <g class="prop-{act}" transform="translate(120,63)">
                    <circle r="19" fill="none" stroke="#7a5810" stroke-width="3"/>
                    <circle r="6"  fill="#7a5810"/>
                    <circle r="3.5" fill="#c8901a"/>
                    <!-- 8 spokes -->
                    <line x1="0" y1="-19" x2="0"   y2="-6"  stroke="#7a5810" stroke-width="2.2"/>
                    <line x1="13" y1="-13" x2="4"  y2="-4"  stroke="#7a5810" stroke-width="2.2"/>
                    <line x1="19" y1="0"   x2="6"  y2="0"   stroke="#7a5810" stroke-width="2.2"/>
                    <line x1="13" y1="13"  x2="4"  y2="4"   stroke="#7a5810" stroke-width="2.2"/>
                    <line x1="0"  y1="19"  x2="0"  y2="6"   stroke="#7a5810" stroke-width="2.2"/>
                    <line x1="-13" y1="13" x2="-4" y2="4"   stroke="#7a5810" stroke-width="2.2"/>
                    <line x1="-19" y1="0"  x2="-6" y2="0"   stroke="#7a5810" stroke-width="2.2"/>
                    <line x1="-13" y1="-13" x2="-4" y2="-4" stroke="#7a5810" stroke-width="2.2"/>
                    <!-- Handle knobs -->
                    <circle cx="0"   cy="-21" r="3" fill="#c8901a"/>
                    <circle cx="15"  cy="-15" r="3" fill="#c8901a"/>
                    <circle cx="21"  cy="0"   r="3" fill="#c8901a"/>
                    <circle cx="15"  cy="15"  r="3" fill="#c8901a"/>
                    <circle cx="0"   cy="21"  r="3" fill="#c8901a"/>
                    <circle cx="-15" cy="15"  r="3" fill="#c8901a"/>
                    <circle cx="-21" cy="0"   r="3" fill="#c8901a"/>
                    <circle cx="-15" cy="-15" r="3" fill="#c8901a"/>
                </g>

            {:else if act === 'reading'}
                <!-- Holographic star chart -->
                <g class="prop-reading" transform="translate(120,60)">
                    <rect x="-24" y="-20" width="48" height="38" rx="3" fill="#081828" stroke="#38b8e0" stroke-width="1.2"/>
                    <line x1="-20" y1="-12" x2="20" y2="-12" stroke="#38b8e0" stroke-width="0.5" opacity="0.45"/>
                    <line x1="-20" y1="-4"  x2="20" y2="-4"  stroke="#38b8e0" stroke-width="0.5" opacity="0.45"/>
                    <line x1="-20" y1="4"   x2="20" y2="4"   stroke="#38b8e0" stroke-width="0.5" opacity="0.45"/>
                    <line x1="-20" y1="12"  x2="20" y2="12"  stroke="#38b8e0" stroke-width="0.5" opacity="0.45"/>
                    <line x1="-12" y1="-16" x2="-12" y2="16" stroke="#38b8e0" stroke-width="0.5" opacity="0.45"/>
                    <line x1="0"   y1="-16" x2="0"   y2="16" stroke="#38b8e0" stroke-width="0.5" opacity="0.45"/>
                    <line x1="12"  y1="-16" x2="12"  y2="16" stroke="#38b8e0" stroke-width="0.5" opacity="0.45"/>
                    <!-- Stars on chart -->
                    <circle cx="-10" cy="-9"  r="1.8" fill="#38b8e0"/>
                    <circle cx="6"   cy="6"   r="1.2" fill="#b0e840"/>
                    <circle cx="14"  cy="-14" r="1.5" fill="#38b8e0"/>
                    <circle cx="-16" cy="9"   r="1.2" fill="#e8c840"/>
                    <circle cx="0"   cy="-3"  r="2.5" fill="#ff8840" opacity="0.85"/>
                    <!-- Route -->
                    <path d="M-16,9 L0,-3 L6,6 L14,-14" stroke="#38b8e0" stroke-width="0.8" fill="none" stroke-dasharray="2,1.5" opacity="0.75"/>
                    <!-- Glow -->
                    <rect x="-24" y="-20" width="48" height="38" rx="3" fill="none" stroke="#38b8e0" stroke-width="2" opacity="0.35">
                        <animate attributeName="opacity" values="0.35;0.8;0.35" dur="1.6s" repeatCount="indefinite"/>
                    </rect>
                </g>

            {:else if act === 'writing'}
                <!-- Gears and wrench -->
                <g class="prop-writing" transform="translate(120,63)">
                    <circle r="17" fill="none" stroke="#7a5810" stroke-width="5" stroke-dasharray="5,3"/>
                    <circle r="11" fill="#4a2e08"/>
                    <circle r="5"  fill="#7a5810"/>
                    <circle r="2.5" fill="#c8901a"/>
                    <rect x="-3" y="-24" width="6" height="32" rx="3" fill="#686868" transform="rotate(38)"/>
                    <ellipse cx="-13" cy="18" rx="6" ry="4" fill="#505050" transform="rotate(38,-13,18)"/>
                    <ellipse cx="-2" cy="-24" rx="5" ry="3.5" fill="#585858" transform="rotate(38)"/>
                    <line x1="8"  y1="-8"  x2="15" y2="-16" stroke="#FFD700" stroke-width="1.8" stroke-linecap="round" class="sp-a"/>
                    <line x1="10" y1="-4"  x2="19" y2="-8"  stroke="#FFA020" stroke-width="1.2" stroke-linecap="round" class="sp-b"/>
                    <line x1="6"  y1="-13" x2="11" y2="-20" stroke="#FFD700" stroke-width="1.4" stroke-linecap="round" class="sp-c"/>
                </g>

            {:else if act === 'searching'}
                <!-- Brass telescope -->
                <g class="prop-searching" transform="translate(120,63)">
                    <rect x="-28" y="-6" width="44" height="12" rx="6" fill="#7a5810"/>
                    <rect x="-22" y="-5" width="7"  height="10" rx="1.5" fill="#c8901a"/>
                    <rect x="-8"  y="-5" width="7"  height="10" rx="1.5" fill="#c8901a"/>
                    <rect x="6"   y="-5" width="7"  height="10" rx="1.5" fill="#c8901a"/>
                    <!-- Lens -->
                    <ellipse cx="-28" cy="0" rx="3.5" ry="8" fill="#1a3858" stroke="#c8901a" stroke-width="1.2"/>
                    <ellipse cx="-28" cy="0" rx="2"   ry="5" fill="#3888c0" opacity="0.7"/>
                    <!-- Eyepiece -->
                    <ellipse cx="18"  cy="0" rx="5"   ry="5" fill="#4a2808"/>
                    <ellipse cx="18"  cy="0" rx="3"   ry="3" fill="#201408"/>
                    <!-- Tripod -->
                    <line x1="-4" y1="6" x2="-12" y2="24" stroke="#4a2808" stroke-width="1.8" stroke-linecap="round"/>
                    <line x1="-4" y1="6" x2="4"   y2="24" stroke="#4a2808" stroke-width="1.8" stroke-linecap="round"/>
                    <line x1="-12" y1="24" x2="4" y2="24" stroke="#4a2808" stroke-width="1.2" stroke-linecap="round"/>
                </g>

            {:else if act === 'analyzing'}
                <!-- Compass -->
                <g class="prop-analyzing" transform="translate(120,63)">
                    <circle r="20" fill="#181408" stroke="#7a5810" stroke-width="2.5"/>
                    <circle r="17" fill="none"   stroke="#c8901a" stroke-width="0.6" opacity="0.4"/>
                    <text x="-3"  y="-12" font-size="6" fill="#c8901a" font-family="serif" font-weight="bold">N</text>
                    <text x="-3"  y="18"  font-size="6" fill="#7a5810" font-family="serif">S</text>
                    <text x="-17" y="4"   font-size="6" fill="#7a5810" font-family="serif">W</text>
                    <text x="11"  y="4"   font-size="6" fill="#7a5810" font-family="serif">E</text>
                    <!-- Needle -->
                    <polygon points="0,-15 -2,0 0,-3 2,0"  fill="#e03020" class="compass-needle"/>
                    <polygon points="0,15  -2,0 0,3  2,0"  fill="#c0c0c0" class="compass-needle"/>
                    <circle r="2.5" fill="#c8901a"/>
                </g>

            {:else if act === 'waiting'}
                <!-- Hourglass -->
                <g class="prop-waiting" transform="translate(120,63)">
                    <line x1="-13" y1="-22" x2="13" y2="-22" stroke="#7a5810" stroke-width="3" stroke-linecap="round"/>
                    <line x1="-13" y1="22"  x2="13" y2="22"  stroke="#7a5810" stroke-width="3" stroke-linecap="round"/>
                    <line x1="-13" y1="-22" x2="-13" y2="22" stroke="#7a5810" stroke-width="1.5"/>
                    <line x1="13"  y1="-22" x2="13"  y2="22" stroke="#7a5810" stroke-width="1.5"/>
                    <polygon points="-11,-21 11,-21 2,-2 -2,-2" fill="#e8c060" opacity="0.85"/>
                    <ellipse cx="0" cy="18" rx="9" ry="3.5" fill="#e8c060" opacity="0.85"/>
                    <line x1="0" y1="-2" x2="0" y2="16" stroke="#e8c060" stroke-width="1.2" opacity="0.6">
                        <animate attributeName="opacity" values="0.6;0.15;0.6" dur="0.9s" repeatCount="indefinite"/>
                    </line>
                    <path d="M-11,-20 Q0,-8 11,-20" fill="#5080a0" opacity="0.2"/>
                    <path d="M-11,20  Q0,8  11,20"  fill="#5080a0" opacity="0.2"/>
                </g>

            {:else if act === 'sleeping'}
                <!-- Dimmed lantern -->
                <g class="prop-sleeping" transform="translate(120,63)">
                    <path d="M-5,-20 Q0,-28 5,-20" fill="none" stroke="#7a5810" stroke-width="1.8"/>
                    <rect x="-11" y="-20" width="22" height="30" rx="3" fill="#321808" stroke="#7a5810" stroke-width="1.5"/>
                    <rect x="-8"  y="-16" width="5"  height="22" rx="1" fill="#c8a820" opacity="0.15"/>
                    <rect x="-1"  y="-16" width="5"  height="22" rx="1" fill="#c8a820" opacity="0.15"/>
                    <rect x="6"   y="-16" width="5"  height="22" rx="1" fill="#c8a820" opacity="0.15"/>
                    <ellipse cx="0" cy="-2" rx="4" ry="6" fill="#c8a820" opacity="0.2">
                        <animate attributeName="opacity" values="0.2;0.08;0.2" dur="2.2s" repeatCount="indefinite"/>
                    </ellipse>
                    <rect x="-13" y="10"  width="26" height="4" rx="2" fill="#7a5810"/>
                    <rect x="-9"  y="-22" width="18" height="4" rx="2" fill="#7a5810"/>
                </g>

            {:else if act === 'error'}
                <!-- Pressure gauge exploding -->
                <g class="prop-error">
                    <rect x="103" y="55" width="34" height="14" rx="5" fill="#484848"/>
                    <rect x="99"  y="57" width="8"  height="10" rx="2" fill="#383838"/>
                    <rect x="137" y="57" width="8"  height="10" rx="2" fill="#383838"/>
                    <circle cx="120" cy="62" r="13" fill="#e0e0d8" stroke="#484848" stroke-width="2.5"/>
                    <circle cx="120" cy="62" r="11" fill="#d0d0c8" stroke="#686868" stroke-width="0.8"/>
                    <text x="115" y="66" font-size="7" fill="#880000" font-weight="bold">!!!</text>
                    <line x1="120" y1="62" x2="128" y2="53" stroke="#d02010" stroke-width="2" stroke-linecap="round"/>
                    <!-- Steam vents -->
                    <path d="M122,49 Q125,41 121,36 Q118,31 122,26" stroke="#c8d8e8" stroke-width="2.2" fill="none" stroke-linecap="round" opacity="0.7">
                        <animate attributeName="opacity" values="0.7;0.15;0.7" dur="0.28s" repeatCount="indefinite"/>
                    </path>
                    <path d="M127,51 Q131,43 128,36" stroke="#c8d8e8" stroke-width="1.6" fill="none" stroke-linecap="round" opacity="0.6">
                        <animate attributeName="opacity" values="0.6;0.1;0.6" dur="0.22s" repeatCount="indefinite"/>
                    </path>
                    <line x1="110" y1="51" x2="104" y2="44" stroke="#FFD700" stroke-width="1.8" stroke-linecap="round" class="sp-a"/>
                    <line x1="112" y1="49" x2="108" y2="42" stroke="#FF8800" stroke-width="1.2" stroke-linecap="round" class="sp-b"/>
                    <line x1="130" y1="51" x2="136" y2="44" stroke="#FFD700" stroke-width="1.8" stroke-linecap="round" class="sp-c"/>
                </g>

            {:else if act === 'starting'}
                <!-- Solar ether sails -->
                <g class="prop-starting" transform="translate(120,55)">
                    <line x1="0" y1="-32" x2="0" y2="28" stroke="#4a2808" stroke-width="3.5" stroke-linecap="round"/>
                    <line x1="-30" y1="-22" x2="30" y2="-22" stroke="#4a2808" stroke-width="2.5" stroke-linecap="round"/>
                    <path d="M-28,-22 Q-22,-2 -6,16 L0,16 L0,-22 Z" fill="url(#tpSail)" opacity="0.85"/>
                    <path d="M28,-22  Q22,-2  6,16  L0,16 L0,-22 Z" fill="url(#tpSail)" opacity="0.85">
                        <animate attributeName="opacity" values="0.85;0.45;0.85" dur="0.9s" repeatCount="indefinite"/>
                    </path>
                    <path d="M-28,-22 Q-22,-2 -6,16" stroke="#50d8f8" stroke-width="1.5" fill="none" opacity="0.9"/>
                    <path d="M28,-22  Q22,-2  6,16"  stroke="#50d8f8" stroke-width="1.5" fill="none" opacity="0.9"/>
                    <line x1="-28" y1="-22" x2="-10" y2="22" stroke="#4a2808" stroke-width="0.8" opacity="0.5"/>
                    <line x1="28"  y1="-22" x2="10"  y2="22" stroke="#4a2808" stroke-width="0.8" opacity="0.5"/>
                </g>
            {/if}

            <!-- ══════════════════════════════════════
                 SILVER  (quartermaster, x=183)
                 ══════════════════════════════════════ -->
            <g class="c2-{act}">
                <!-- Boots -->
                <rect x="171" y="77" width="11" height="13" rx="2" fill="#241608"/>
                <rect x="184" y="77" width="11" height="13" rx="2" fill="#241608"/>
                <rect x="173" y="83" width="6"  height="2"  rx="0.5" fill="#7a5010"/>
                <rect x="186" y="83" width="6"  height="2"  rx="0.5" fill="#7a5010"/>
                <!-- Legs -->
                <rect x="172" y="66" width="10" height="13" fill="#361508"/>
                <rect x="185" y="66" width="10" height="13" fill="#361508"/>
                <!-- Belt -->
                <rect x="169" y="63" width="34" height="4" fill="#321808"/>
                <rect x="180" y="62" width="7"  height="6"  rx="1" fill="#c8901a"/>
                <!-- Coat body (dark red) -->
                <rect x="168" y="46" width="36" height="20" rx="3" fill="#501408"/>
                <!-- Shirt visible (V) -->
                <polygon points="180,46 188,46 186,62 182,62" fill="#d0c090"/>
                <!-- Coat lapels -->
                <polygon points="180,46 168,55 175,60" fill="#3c1006"/>
                <polygon points="188,46 204,55 197,60" fill="#3c1006"/>
                <!-- Brass buttons -->
                <circle cx="184" cy="51" r="2"   fill="#c8901a"/>
                <circle cx="184" cy="58" r="2"   fill="#c8901a"/>
                <!-- Epaulettes -->
                <rect x="165" y="45" width="9"  height="5" rx="2" fill="#7a5810"/>
                <rect x="192" y="45" width="9"  height="5" rx="2" fill="#7a5810"/>
                <!-- Left arm (normal, skin) -->
                <rect x="148" y="49" width="20" height="7" rx="3.5" fill="#a86030"/>
                <ellipse cx="148" cy="52" rx="4" ry="4.5" fill="#a86030"/>
                <!-- Mechanical right arm -->
                <ellipse cx="202" cy="52" rx="5" ry="5.5" fill="#707070" stroke="#484848" stroke-width="1"/>
                <circle  cx="202" cy="52" r="3"  fill="#505050"/>
                <circle  cx="202" cy="52" r="1.5" fill="#7a5810"/>
                <rect x="206" y="49" width="20" height="7" rx="3" fill="#686868" stroke="#484848" stroke-width="0.8"/>
                <rect x="218" y="52" width="13" height="6" rx="3" fill="#585858" stroke="#383838" stroke-width="0.7" transform="rotate(12,218,55)"/>
                <line x1="229" y1="55" x2="234" y2="50" stroke="#484848" stroke-width="2.2" stroke-linecap="round"/>
                <line x1="229" y1="57" x2="235" y2="57" stroke="#484848" stroke-width="2.2" stroke-linecap="round"/>
                <!-- Head -->
                <ellipse cx="183" cy="35" rx="15" ry="14" fill="#a86030"/>
                <!-- Beard / lower face stubble -->
                <ellipse cx="183" cy="46" rx="10" ry="7" fill="#361808"/>
                <!-- Pirate bandana -->
                <ellipse cx="183" cy="23" rx="15" ry="5"  fill="#281008"/>
                <path d="M168,24 Q175,18 181,21" fill="#281008"/>
                <ellipse cx="183" cy="20" rx="13" ry="5"  fill="#1e0c06"/>
                <!-- Bandana knot at top -->
                <ellipse cx="183" cy="16" rx="4"  ry="3"  fill="#321410"/>
                <line x1="180" y1="14" x2="186" y2="14"  stroke="#201008" stroke-width="2" stroke-linecap="round"/>
                <!-- Bandana tails hanging right -->
                <path d="M193,22 L198,34 L194,40" fill="#281008"/>
                <path d="M193,22 L197,36 L193,42" fill="#281008" opacity="0.75"/>
                <!-- Mechanical LEFT eye -->
                <circle cx="176" cy="33" r="8"   fill="#282828" stroke="#484848" stroke-width="1.2"/>
                <circle cx="176" cy="33" r="5.5" fill="#181818"/>
                <circle cx="176" cy="33" r="3.5" fill="#a06020"/>
                <circle cx="176" cy="33" r="1.8" fill="#800000"/>
                <line x1="168" y1="33" x2="184" y2="33" stroke="#808000" stroke-width="0.5" opacity="0.6"/>
                <line x1="176" y1="25" x2="176" y2="41" stroke="#808000" stroke-width="0.5" opacity="0.6"/>
                <circle cx="176" cy="33" r="8"   fill="none" stroke="#7a5810" stroke-width="1.8"/>
                <!-- Normal RIGHT eye -->
                <ellipse cx="189" cy="33" rx="4" ry="3.5" fill="#241408"/>
                <ellipse cx="189" cy="33" rx="2.2" ry="2" fill="#4a2808"/>
                <circle  cx="188.5" cy="32" r="0.9" fill="white" opacity="0.45"/>
                <!-- Eyebrows -->
                <path d="M172,25 Q176,22 180,26" stroke="#241408" stroke-width="1.8" fill="none" stroke-linecap="round"/>
                <rect x="184" y="27" width="9" height="2.5" rx="1" fill="#241408"/>
                <!-- Nose -->
                <ellipse cx="183" cy="41" rx="4.5" ry="3.5" fill="#904818"/>
                <!-- Ear -->
                <ellipse cx="198" cy="35" rx="3" ry="4" fill="#985028"/>
                <!-- Mouth -->
                {#if act === 'error'}
                    <ellipse cx="183" cy="49" rx="6" ry="3.5" fill="#241408"/>
                {:else if act === 'sleeping'}
                    <path d="M177,49 Q183,54 189,49" stroke="#6a3010" stroke-width="1.8" fill="none" stroke-linecap="round"/>
                {:else if act === 'waiting'}
                    <line x1="177" y1="49" x2="189" y2="49" stroke="#6a3010" stroke-width="1.8" stroke-linecap="round"/>
                {:else}
                    <path d="M176,48 Q183,54 190,48" stroke="#6a3010" stroke-width="1.8" fill="none" stroke-linecap="round"/>
                {/if}
            </g>

            <!-- ── SPEECH BUBBLE (parchment) ── -->
            <rect x="58" y="2" width="124" height="22" rx="4" fill="url(#tpParch)" stroke="#7a5810" stroke-width="1.8"/>
            <polygon points="74,24 65,37 90,24" fill="#eedd98"/>
            <line x1="65" y1="37" x2="74" y2="24" stroke="#7a5810" stroke-width="1.5"/>
            <line x1="65" y1="37" x2="90" y2="24" stroke="#7a5810" stroke-width="1.5"/>
            <text x="60"  y="16" font-size="8" fill="#7a5810" opacity="0.5">⚓</text>
            <text x="170" y="16" font-size="8" fill="#7a5810" opacity="0.5">✦</text>
            <text x="120" y="16"
                  text-anchor="middle"
                  font-size="7.5"
                  font-family="Georgia, serif"
                  font-weight="bold"
                  font-style="italic"
                  fill="#281400">{label}</text>

            <!-- ── SLEEPING ZZZs ── -->
            {#if act === 'sleeping'}
                <text x="92"  y="48" font-size="9"  fill="#8898b0" font-family="Georgia,serif" font-style="italic" class="zzz-a">z</text>
                <text x="104" y="40" font-size="11" fill="#8898b0" font-family="Georgia,serif" font-style="italic" class="zzz-b">z</text>
                <text x="118" y="32" font-size="13" fill="#a0b0c8" font-family="Georgia,serif" font-style="italic" class="zzz-c">Z</text>
            {/if}
        </svg>
    </div>
</div>

<style>
    .tp-panel {
        border: 2.5px solid #7a5810;
        border-radius: 4px;
        overflow: hidden;
        margin: 6px 6px 4px;
        box-shadow: 3px 3px 0 #1a0a00, 0 0 10px rgba(180,120,20,0.25);
        flex-shrink: 0;
    }
    .tp-title {
        background: linear-gradient(to bottom, #3d2008, #281400);
        color: #c8901a;
        font-size: 8px;
        font-weight: 900;
        letter-spacing: 0.08em;
        text-align: center;
        padding: 2px 4px;
        border-bottom: 2px solid #7a5810;
        font-family: Georgia, serif;
        text-shadow: 0 0 8px rgba(200,144,26,0.6);
        display: flex;
        align-items: center;
        justify-content: center;
        position: relative;
    }
    .tp-demo-btn {
        position: absolute;
        right: 5px;
        top: 50%;
        transform: translateY(-50%);
        background: none;
        border: 1px solid #7a5810;
        color: #c8901a;
        font-size: 7px;
        line-height: 1;
        padding: 1px 4px;
        border-radius: 2px;
        cursor: pointer;
        opacity: 0.6;
        font-family: monospace;
        transition: opacity 0.15s;
    }
    .tp-demo-btn:hover:not(:disabled) { opacity: 1; border-color: #c8901a; }
    .tp-demo-btn:disabled { opacity: 0.35; cursor: default; }
    .tp-demo-running { opacity: 0.5; }
    .tp-body { padding: 0; }

    /* ── Static animation classes ── */
    .zzz-a { animation: zzz-rise 2s ease-in infinite; }
    .zzz-b { animation: zzz-rise 2s ease-in infinite 0.65s; }
    .zzz-c { animation: zzz-rise 2s ease-in infinite 1.3s; }
    .sp-a  { animation: spark-flash 0.18s ease-in-out infinite; }
    .sp-b  { animation: spark-flash 0.18s ease-in-out infinite 0.07s; }
    .sp-c  { animation: spark-flash 0.18s ease-in-out infinite 0.14s; }
    .compass-needle { animation: compass-spin 6s linear infinite; transform-origin: 120px 63px; }

    /* ── Jim (c1) ── */
    :global(.c1-idle)      { animation: sail-sway    3s   ease-in-out infinite; }
    :global(.c1-reading)   { animation: chart-lean   2s   ease-in-out infinite; transform-origin: 52px 90px; }
    :global(.c1-writing)   { animation: fix-bob      0.45s ease-in-out infinite; }
    :global(.c1-searching) { animation: scan-look    2.2s ease-in-out infinite; }
    :global(.c1-executing) { animation: helm-grip    0.28s ease-in-out infinite; }
    :global(.c1-analyzing) { animation: chart-lean   2.5s ease-in-out infinite; transform-origin: 52px 90px; }
    :global(.c1-waiting)   { animation: impatient    1s   ease-in-out infinite; }
    :global(.c1-sleeping)  { animation: sleep-nod    4s   ease-in-out infinite; transform-origin: 52px 90px; }
    :global(.c1-error)     { animation: panic        0.09s ease-in-out infinite; }
    :global(.c1-starting)  { animation: rush-up      0.4s ease-in-out infinite; }

    /* ── Silver (c2) ── */
    :global(.c2-idle)      { animation: sail-sway    3s   ease-in-out infinite 1.2s; }
    :global(.c2-reading)   { animation: approve-nod  2.2s ease-in-out infinite 0.3s; transform-origin: 183px 90px; }
    :global(.c2-writing)   { animation: arm-heave    0.6s ease-in-out infinite 0.15s; }
    :global(.c2-searching) { animation: scan-look    2.5s ease-in-out infinite 0.5s; }
    :global(.c2-executing) { animation: helm-grip    0.32s ease-in-out infinite 0.1s; }
    :global(.c2-analyzing) { animation: sail-sway    2.5s ease-in-out infinite 0.5s; }
    :global(.c2-waiting)   { animation: impatient    1.2s ease-in-out infinite 0.2s; }
    :global(.c2-sleeping)  { animation: sleep-nod    4.5s ease-in-out infinite 0.6s; transform-origin: 183px 90px; }
    :global(.c2-error)     { animation: panic        0.11s ease-in-out infinite 0.04s; }
    :global(.c2-starting)  { animation: rush-up      0.4s ease-in-out infinite 0.2s; }

    /* ── Props ── */
    :global(.prop-idle)      { animation: wheel-slow  4s linear       infinite; transform-origin: 120px 63px; }
    :global(.prop-executing) { animation: wheel-fast  0.6s linear     infinite; transform-origin: 120px 63px; }
    :global(.prop-reading)   { animation: chart-pulse 1.6s ease-in-out infinite; }
    :global(.prop-writing)   { animation: gear-turn   1.1s linear     infinite; transform-origin: 120px 63px; }
    :global(.prop-searching) { animation: scope-sweep 2.2s ease-in-out infinite; }
    :global(.prop-analyzing) { animation: sail-sway   2s ease-in-out  infinite; }
    :global(.prop-waiting)   { animation: hg-pulse    0.8s ease-in-out infinite; }
    :global(.prop-sleeping)  { animation: lamp-dim    2.5s ease-in-out infinite; }
    :global(.prop-error)     { animation: gauge-shake 0.08s ease-in-out infinite; }
    :global(.prop-starting)  { animation: sail-billow 1.1s ease-in-out infinite; transform-origin: 120px 55px; }

    /* ── Keyframes ── */
    @keyframes sail-sway {
        0%, 100% { transform: translateY(0); }
        50%      { transform: translateY(-6px); }
    }
    @keyframes chart-lean {
        0%, 100% { transform: rotate(-5deg) translateY(0); }
        50%      { transform: rotate(4deg)  translateY(-2px); }
    }
    @keyframes fix-bob {
        0%, 100% { transform: translateY(0) rotate(-1deg); }
        35%      { transform: translateY(-5px) rotate(2deg); }
        70%      { transform: translateY(-2px) rotate(-1deg); }
    }
    @keyframes scan-look {
        0%, 100% { transform: translateX(-7px); }
        50%      { transform: translateX(7px); }
    }
    @keyframes helm-grip {
        0%, 100% { transform: translateY(0); }
        50%      { transform: translateY(-2.5px); }
    }
    @keyframes impatient {
        0%, 70%, 100% { transform: translateY(0); }
        80%           { transform: translateY(-3px); }
        90%           { transform: translateY(0); }
    }
    @keyframes sleep-nod {
        0%, 100% { transform: rotate(0deg)  translateY(0); }
        30%      { transform: rotate(-5deg) translateY(3px); }
        70%      { transform: rotate(4deg)  translateY(3px); }
    }
    @keyframes panic {
        0%, 100% { transform: translate(0,0)     rotate(0deg); }
        20%      { transform: translate(-4px,-3px) rotate(-4deg); }
        40%      { transform: translate(4px,2px)   rotate(4deg); }
        60%      { transform: translate(-3px,3px)  rotate(-3deg); }
        80%      { transform: translate(3px,-2px)  rotate(2deg); }
    }
    @keyframes rush-up {
        0%, 100% { transform: translateY(0); }
        50%      { transform: translateY(-10px); }
    }
    @keyframes approve-nod {
        0%, 100% { transform: rotate(0deg); }
        25%      { transform: rotate(-6deg); }
        60%      { transform: rotate(4deg); }
    }
    @keyframes arm-heave {
        0%, 100% { transform: translateY(0) rotate(0deg); }
        40%      { transform: translateY(-4px) rotate(3deg); }
        70%      { transform: translateY(-2px) rotate(-2deg); }
    }
    @keyframes wheel-slow {
        from { transform: rotate(0deg); }
        to   { transform: rotate(360deg); }
    }
    @keyframes wheel-fast {
        from { transform: rotate(0deg); }
        to   { transform: rotate(360deg); }
    }
    @keyframes chart-pulse {
        0%, 100% { opacity: 1; transform: scale(1); }
        50%      { opacity: 0.8; transform: scale(1.03); }
    }
    @keyframes gear-turn {
        from { transform: rotate(0deg); }
        to   { transform: rotate(360deg); }
    }
    @keyframes scope-sweep {
        0%, 100% { transform: translate(-9px,0) rotate(-12deg); }
        50%      { transform: translate(9px,0)  rotate(12deg); }
    }
    @keyframes hg-pulse {
        0%, 100% { transform: scale(1); }
        50%      { transform: scale(1.04); }
    }
    @keyframes lamp-dim {
        0%, 100% { opacity: 1; }
        50%      { opacity: 0.65; }
    }
    @keyframes gauge-shake {
        0%, 100% { transform: translate(0,0); }
        25%      { transform: translate(-3px,-2px); }
        75%      { transform: translate(3px,2px); }
    }
    @keyframes sail-billow {
        0%, 100% { transform: scaleX(1); }
        50%      { transform: scaleX(1.08); }
    }
    @keyframes compass-spin {
        from { transform: rotate(0deg); }
        to   { transform: rotate(360deg); }
    }
    @keyframes zzz-rise {
        0%   { opacity: 0;   transform: translateY(0)     scale(0.8); }
        15%  { opacity: 1; }
        85%  { opacity: 0.4; }
        100% { opacity: 0;   transform: translateY(-24px) scale(1.2); }
    }
    @keyframes spark-flash {
        0%, 100% { opacity: 1;    transform: scale(1)    rotate(0deg); }
        50%      { opacity: 0.1;  transform: scale(1.7)  rotate(45deg); }
    }
</style>
