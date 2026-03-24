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
  ralph [--plan] [-p TEXT] [FILE] [N]  Agent loop (-p: inline prompt)
  spawn <prompt.md> [N]    Spawn Ralph in a new worktree
  board [--watch]          Task board (read from tasks.json)
  cron-curate              Curate projects with new digests
  doctor                   System health check

Flags:
  --json          Output as JSON
  --help          Show this help
  --version       Show version`

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}

	cmd := os.Args[1]

	switch cmd {
	case "--help", "-h", "help":
		fmt.Println(usage)
		return 0
	case "--version", "-v":
		fmt.Println("sesh", version)
		return 0
	case "digest":
		return runDigest(os.Args[2:])
	case "context":
		return runContext(os.Args[2:])
	case "status":
		return runStatus(os.Args[2:])
	case "fmt":
		return runFmt(os.Args[2:])
	case "ralph":
		return runRalph(os.Args[2:])
	case "spawn":
		return runSpawn(os.Args[2:])
	case "board":
		return runBoard(os.Args[2:])
	case "install":
		return runInstall(os.Args[2:])
	case "cron-curate":
		return runCronCurate(os.Args[2:])
	case "doctor":
		return runDoctor(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "sesh: unknown command %q\n\n%s\n", cmd, usage)
		return 1
	}
}
