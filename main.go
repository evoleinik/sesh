package main

import (
	"fmt"
	"os"
)

const version = "0.1.0"

var usage = `sesh — session intelligence for Claude Code

Usage:
  sesh <command> [flags]

Commands:
  digest <session.jsonl>   Parse session JSONL → digest markdown
  context [project-dir]    Load recent digests → context summary
  status                   Cross-project activity dashboard
  fmt                      Format stream-json from stdin
  install                  One-shot setup (hooks, gitignore, cron)
  cron-curate              Curate projects with new digests

Flags:
  --json          Output as JSON
  --help          Show this help
  --version       Show version`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	cmd := os.Args[1]

	switch cmd {
	case "--help", "-h", "help":
		fmt.Println(usage)
		os.Exit(0)
	case "--version", "-v":
		fmt.Println("sesh", version)
		os.Exit(0)
	case "digest":
		runDigest(os.Args[2:])
	case "context":
		runContext(os.Args[2:])
	case "status":
		runStatus(os.Args[2:])
	case "fmt":
		runFmt(os.Args[2:])
	case "install":
		runInstall(os.Args[2:])
	case "cron-curate":
		runCronCurate(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "sesh: unknown command %q\n\n%s\n", cmd, usage)
		os.Exit(1)
	}
}
