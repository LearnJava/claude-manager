// Short attention sound for a permission request (permission_sound).
// Synthesized with Web Audio so no asset ships with the app. Best effort:
// WebView2 may keep the AudioContext suspended until the user has
// interacted with the page, in which case the beep is silently skipped.

let ctx: AudioContext | null = null;

export function beep(): void {
    try {
        const AC = (window as any).AudioContext ?? (window as any).webkitAudioContext;
        if (!AC) return;
        ctx = ctx ?? new AC();
        const ac = ctx!;
        if (ac.state === 'suspended') void ac.resume();
        // Two short rising tones.
        [660, 880].forEach((freq, i) => {
            const t0 = ac.currentTime + i * 0.16;
            const osc = ac.createOscillator();
            const gain = ac.createGain();
            osc.type = 'sine';
            osc.frequency.value = freq;
            gain.gain.setValueAtTime(0.0001, t0);
            gain.gain.exponentialRampToValueAtTime(0.25, t0 + 0.02);
            gain.gain.exponentialRampToValueAtTime(0.0001, t0 + 0.14);
            osc.connect(gain).connect(ac.destination);
            osc.start(t0);
            osc.stop(t0 + 0.15);
        });
    } catch {
        /* sound is non-critical */
    }
}
