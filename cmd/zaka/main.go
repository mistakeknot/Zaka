// Command zaka steers CLI AI agents via tmux and observes via CASS.
//
// Usage:
//
//	zaka spawn --agent claude-code --workdir .
//	zaka steer <session> "fix the auth bug"
//	zaka observe --timeline 1h
//	zaka list
//	zaka kill <session>
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mistakeknot/Zaka/internal/adapter"
	"github.com/mistakeknot/Zaka/internal/tmux"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	switch os.Args[1] {
	case "spawn":
		cmdSpawn(ctx, os.Args[2:])
	case "steer":
		cmdSteer(ctx, os.Args[2:])
	case "list":
		cmdList(ctx)
	case "kill":
		cmdKill(ctx, os.Args[2:])
	case "agents":
		cmdAgents()
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func cmdSpawn(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("spawn", flag.ExitOnError)
	agentName := fs.String("agent", "claude-code", "Agent adapter name")
	workDir := fs.String("workdir", ".", "Working directory")
	model := fs.String("model", "", "Model override")
	permMode := fs.String("permission-mode", "", "Permission mode")
	name := fs.String("name", "", "Session name override")
	fs.Parse(args)

	a := adapter.Get(*agentName)
	if a == nil {
		log.Fatalf("unknown agent %q — available: %s", *agentName, strings.Join(adapter.List(), ", "))
	}

	cfg := adapter.Config{
		Model:          *model,
		PermissionMode: *permMode,
		SessionName:    *name,
	}

	sess, err := tmux.Spawn(ctx, a, *workDir, cfg)
	if err != nil {
		log.Fatalf("spawn: %v", err)
	}
	fmt.Println(sess.Name)
}

func cmdSteer(ctx context.Context, args []string) {
	if len(args) < 2 {
		log.Fatal("usage: zaka steer <session-name> <prompt>")
	}
	sessionName := args[0]
	prompt := strings.Join(args[1:], " ")

	// We need an adapter to format the prompt, but for steer we just
	// send raw text — all adapters currently pass through.
	sess := &tmux.Session{Name: sessionName}
	cmd := fmt.Sprintf("%s", prompt)

	// Direct send-keys without adapter formatting.
	if err := sess.SendPrompt(ctx, cmd); err != nil {
		log.Fatalf("steer: %v", err)
	}

	// Wait briefly and capture output.
	time.Sleep(1 * time.Second)
	out, err := sess.CapturePane(ctx)
	if err != nil {
		log.Fatalf("capture: %v", err)
	}
	fmt.Print(out)
}

func cmdList(ctx context.Context) {
	sessions, err := tmux.ListSessions(ctx)
	if err != nil {
		log.Fatalf("list: %v", err)
	}
	if len(sessions) == 0 {
		fmt.Println("no active zaka sessions")
		return
	}
	for _, s := range sessions {
		fmt.Println(s)
	}
}

func cmdKill(ctx context.Context, args []string) {
	if len(args) < 1 {
		log.Fatal("usage: zaka kill <session-name>")
	}
	sess := &tmux.Session{Name: args[0]}
	if err := sess.Kill(ctx); err != nil {
		log.Fatalf("kill: %v", err)
	}
	fmt.Printf("killed %s\n", args[0])
}

func cmdAgents() {
	fmt.Println("available agents:")
	for _, name := range adapter.List() {
		a := adapter.Get(name)
		cass := a.CassConnector()
		resume := "no"
		if a.SupportsResume() {
			resume = "yes"
		}
		if cass == "" {
			cass = "(screen scrape)"
		}
		fmt.Printf("  %-15s cass=%-15s resume=%s\n", name, cass, resume)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `zaka — universal CLI agent driver

Commands:
  spawn    Start an agent in a tmux session
  steer    Send a prompt to a running session
  list     List active zaka sessions
  kill     Kill a session
  agents   List available agent adapters

Usage:
  zaka spawn --agent claude-code --workdir .
  zaka steer <session> "fix the auth bug"
  zaka list
  zaka kill <session>
`)
}
