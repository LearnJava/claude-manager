"""t1 verify: copy hidden tests in, run go test, report PASS/FAIL."""
import shutil
import subprocess
import sys
from pathlib import Path

sys.stdout.reconfigure(encoding="utf-8")
HERE = Path(__file__).resolve().parent
SANDBOX = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path.cwd()

def main() -> int:
    shutil.copy2(HERE / "hidden" / "words_hidden_test.go", SANDBOX / "words_hidden_test.go")
    r = subprocess.run(["go", "test", "./..."], cwd=SANDBOX, capture_output=True, timeout=300)
    out = (r.stdout + r.stderr).decode("utf-8", "replace")
    print(out[-1500:])
    if r.returncode != 0:
        print("FAIL: go test exited", r.returncode)
        return 1
    print("PASS")
    return 0

if __name__ == "__main__":
    sys.exit(main())
