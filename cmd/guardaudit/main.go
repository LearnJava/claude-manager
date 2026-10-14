// Command guardaudit replays the command guard over saved Claude sessions and
// lists the failures it does not cover (internal/guardaudit).
//
//	go run ./cmd/guardaudit [flags] [root ...]
//
// Roots default to the CLI's transcript directory (~/.claude/projects). Saved
// run logs (<project>/.claude-manager/logs, or the mirrors under projects/)
// can be passed as extra roots. Exit code 2 means some rule would rewrite a
// command that worked — review each listed one before shipping the rule.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"claude-manager/internal/experience"
	"claude-manager/internal/guardaudit"
)

func main() {
	project := flag.String("project", "", "only files whose path contains this substring")
	since := flag.String("since", "", "only files modified on or after this date (YYYY-MM-DD)")
	top := flag.Int("top", 30, "number of uncovered failure clusters to list")
	examples := flag.Int("examples", 3, "examples per rule and per cluster")
	asJSON := flag.Bool("json", false, "print the full report as JSON")
	exclude := flag.String("exclude", `AppData-Local-Temp,AppData\Local\Temp`,
		"comma-separated path substrings to skip (default: projects under %TEMP%, i.e. test runs)")
	home, _ := os.UserHomeDir()
	guardLog := flag.String("guardlog", filepath.Join(home, ".claude-manager", "logs", "cmdguard.log"),
		"the hook's log; commands it already rewrote are counted as live, not as working")
	flag.Parse()

	opts := guardaudit.Options{Project: *project, Top: *top, Examples: *examples}
	for _, x := range strings.Split(*exclude, ",") {
		if x = strings.TrimSpace(x); x != "" {
			opts.Exclude = append(opts.Exclude, x)
		}
	}
	live, err := guardaudit.LoadGuardLog(*guardLog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "guard log:", err)
		os.Exit(1)
	}
	opts.LiveFixed = live
	if *since != "" {
		t, err := time.ParseInLocation("2006-01-02", *since, time.Local)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bad -since:", err)
			os.Exit(1)
		}
		opts.Since = t
	}
	roots := flag.Args()
	if len(roots) == 0 {
		roots = []string{experience.TranscriptsRoot()}
	}

	rep, err := guardaudit.Scan(roots, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "scan:", err)
		os.Exit(1)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
	} else {
		printReport(rep)
	}
	if len(rep.Broken) > 0 {
		os.Exit(2)
	}
}

func printReport(rep guardaudit.Report) {
	fmt.Printf("files %d (unreadable %d), tool calls %d, Bash %d, failed %d, Bash cwd gone %d\n\n",
		rep.Files, rep.ReadErrors, rep.ToolCalls, rep.BashCalls, rep.Failed, rep.CwdGone)

	fmt.Println("Guard rules (rewrites; on_working must be 0):")
	if len(rep.Rules) == 0 {
		fmt.Println("  none fired")
	}
	for _, r := range rep.Rules {
		fmt.Printf("  %-40s fixed %5d  on_failed %5d  live %5d  on_working %d\n", r.Rule, r.Fixed, r.OnFailed, r.Live, r.OnWorking)
		for _, ex := range r.Examples {
			fmt.Printf("      %s\n   -> %s\n", oneLine(ex.Command), oneLine(ex.Fixed))
		}
	}
	if len(rep.Broken) > 0 {
		fmt.Printf("\nREWRITES OF WORKING COMMANDS: %d\n", len(rep.Broken))
		for i, ex := range rep.Broken {
			if i == 10 {
				fmt.Printf("  ... %d more (-json for all)\n", len(rep.Broken)-10)
				break
			}
			fmt.Printf("  [%s] %s\n   -> %s\n", ex.File, oneLine(ex.Command), oneLine(ex.Fixed))
		}
	}

	fmt.Println("\nFailures the guard does not cover (by distinct sessions):")
	for _, c := range rep.Uncovered {
		fmt.Printf("  %4d sess %5d calls  %-10s %s\n", c.Sessions, c.Count, c.Tool, c.Key)
		for _, ex := range c.Examples {
			fmt.Printf("      $ %s\n", oneLine(ex.Command))
		}
	}
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	if r := []rune(s); len(r) > 160 {
		s = string(r[:160]) + "…"
	}
	return s
}
