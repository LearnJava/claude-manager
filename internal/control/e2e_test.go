package control

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"claude-manager/internal/config"
	"claude-manager/internal/session"
	"claude-manager/internal/testkit"
)

// projectRoot returns the project root directory, derived from the working
// directory that go test sets when running this package.
func projectRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		panic("e2e: os.Getwd: " + err.Error())
	}
	// wd is .../claude-manager/internal/control
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

// buildFakeclaude compiles cmd/fakeclaude into a temp binary and returns its
// path. The binary is cleaned up automatically by t.TempDir.
func buildFakeclaude(t *testing.T) string {
	t.Helper()

	// Check that the Go toolchain is available.
	goExe, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not in PATH, skipping e2e: " + err.Error())
	}

	binName := "fakeclaude"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	outPath := filepath.Join(t.TempDir(), binName)
	root := projectRoot()

	cmd := exec.Command(goExe, "build", "-o", outPath, "./cmd/fakeclaude/")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakeclaude: %v\n%s", err, out)
	}
	return outPath
}

// newE2EHarness loads the TOML at cfgPath, patches it for test use
// (fast retries, no crash recovery, zero start delay, fakeclaude path), and
// starts an httptest.Server backed by a real SessionManager and ControlEmitter.
// When workerURL is non-empty, every [[worker]] is pointed at it (a fakeworker)
// and its key env var is set to a dummy value.
// Returns the server and the auth token. Both are cleaned up with t.Cleanup.
func newE2EHarness(t *testing.T, cfgPath, claudePath, projectDir, workerURL string) (*httptest.Server, string) {
	t.Helper()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config %s: %v", cfgPath, err)
	}

	// Override settings for deterministic tests.
	cfg.Settings.ClaudePath = claudePath
	cfg.Settings.DefaultRetryDelay = 1
	cfg.Settings.RateLimitPause = 1
	cfg.Settings.CrashRecovery = false
	cfg.Settings.SessionStartDelay = 0 // bypass the default-3 applied by applyDefaults

	// Point all projects at a real directory so the subprocess can start.
	for i := range cfg.Projects {
		cfg.Projects[i].Path = projectDir
	}

	// Point all workers at the fakeworker server started for this scenario.
	if workerURL != "" {
		for i := range cfg.Workers {
			cfg.Workers[i].BaseURL = workerURL
			t.Setenv(cfg.Workers[i].KeyEnv, "fakeworker-e2e-key")
		}
	}

	// Use a temp dir as the state-file directory (cfgPath determines the dir).
	tmpDir := t.TempDir()
	synthCfgPath := filepath.Join(tmpDir, "config.toml")

	ce := NewControlEmitter(500)
	mgr := session.NewSessionManager(cfg, synthCfgPath, nil, ce)

	const token = "e2e-test-token"
	srv := NewServer(mgr, nil, ce, token)

	// httptest.NewServer uses srv.srv.Handler which is accessible inside the
	// same package (control).
	ts := httptest.NewServer(srv.srv.Handler)

	t.Cleanup(func() {
		mgr.StopAll()
		ts.Close()
	})
	return ts, token
}

// TestE2E builds fakeclaude once and then runs every scenario in
// testdata/e2e/ through the runner. Each scenario is a sub-test so failures
// are isolated and reported individually.
func TestE2E(t *testing.T) {
	root := projectRoot()
	e2eDir := filepath.Join(root, "testdata", "e2e")
	scenariosDir := filepath.Join(root, "testdata", "scenarios")

	scenarios, err := LoadE2EScenariosDir(e2eDir)
	if err != nil {
		t.Fatalf("load e2e dir %s: %v", e2eDir, err)
	}
	if len(scenarios) == 0 {
		t.Fatalf("no e2e scenarios found in %s", e2eDir)
	}

	claudePath := buildFakeclaude(t)

	// FAKECLAUDE_SCENARIO is inherited by all subprocess invocations spawned
	// by the session manager during the test.
	t.Setenv("FAKECLAUDE_SCENARIO", scenariosDir)

	for _, sc := range scenarios {
		sc := sc // capture
		t.Run(sc.Name, func(t *testing.T) {
			cfgPath := filepath.Join(root, sc.Config)
			projectDir, workerURL := root, ""
			if sc.WorkerScenario != "" {
				workerURL = startFakeWorker(t, filepath.Join(root, sc.WorkerScenario))
				projectDir = newSeededGitRepo(t, sc.SeedFiles)
			}
			ts, token := newE2EHarness(t, cfgPath, claudePath, projectDir, workerURL)
			runner := NewE2ERunner(ts.URL, token)
			if err := runner.Run(sc); err != nil {
				t.Fatalf("scenario %q failed: %v", sc.Name, err)
			}
		})
	}
}

// startFakeWorker serves the worker scenario at scenarioPath over httptest and
// returns its base URL (cleaned up with t.Cleanup).
func startFakeWorker(t *testing.T, scenarioPath string) string {
	t.Helper()
	sc, err := testkit.LoadWorkerScenario(scenarioPath)
	if err != nil {
		t.Fatalf("load worker scenario %s: %v", scenarioPath, err)
	}
	srv := httptest.NewServer(testkit.NewFakeWorker(sc))
	t.Cleanup(srv.Close)
	return srv.URL
}

// newSeededGitRepo creates a temp git repository containing files (relative
// path -> content) in a single seed commit, so `git worktree add` and worker
// patches have a HEAD and anchors to work against.
func newSeededGitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH, skipping mixed e2e: " + err.Error())
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "e2e@test.local")
	run("config", "user.name", "e2e")
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-q", "-m", "seed")
	return dir
}
