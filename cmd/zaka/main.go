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
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mistakeknot/Zaka/internal/adapter"
	"github.com/mistakeknot/Zaka/internal/appserver"
	"github.com/mistakeknot/Zaka/internal/tmux"
)

type stringListFlag []string

func (values *stringListFlag) String() string {
	return strings.Join(*values, " ")
}

func (values *stringListFlag) Set(value string) error {
	*values = append(*values, value)
	return nil
}

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
	case "status", "questions", "answer":
		cmdAppServer(ctx, os.Args[1], os.Args[2:])
	case "_app-server-worker":
		if len(os.Args) != 4 {
			log.Fatal("internal worker requires state root and handle")
		}
		ready := os.NewFile(3, "ready")
		if ready == nil {
			log.Fatal("internal worker requires readiness pipe")
		}
		if err := appserver.RunWorker(ctx, os.Args[2], os.Args[3], ready); err != nil {
			log.Fatal(err)
		}
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
	var agentArgs stringListFlag
	agentName := fs.String("agent", "claude-code", "Agent adapter name")
	workDir := fs.String("workdir", ".", "Working directory")
	model := fs.String("model", "", "Model override")
	permMode := fs.String("permission-mode", "", "Permission mode")
	name := fs.String("name", "", "Session name override")
	transport := fs.String("transport", "tmux", "Transport: tmux or app-server (Codex only)")
	sandbox := fs.String("sandbox", "", "App Server sandbox: read-only or workspace-write")
	approval := fs.String("approval-policy", "", "App Server approval policy: untrusted, on-request, never")
	metadata := fs.String("metadata-json", "", "Opaque parent routing metadata (JSON)")
	fs.Var(&agentArgs, "agent-arg", "Additional agent argument (repeatable)")
	fs.Parse(args)
	if fs.NArg() != 0 {
		log.Fatal("spawn: unexpected positional arguments")
	}
	if *transport == "app-server" {
		if *agentName != "codex" {
			log.Fatal("app-server transport supports only --agent codex")
		}
		if *name != "" || *permMode != "" {
			log.Fatal("app-server generates a handle and requires --sandbox/--approval-policy; --name and --permission-mode are tmux-only")
		}
		handle, err := appserver.Spawn(ctx, appServerRoot(), appserver.Config{WorkDir: *workDir, Model: *model, Sandbox: *sandbox, ApprovalPolicy: *approval, AgentArgs: []string(agentArgs), Metadata: json.RawMessage(*metadata)})
		if err != nil {
			log.Fatalf("spawn: %v", err)
		}
		fmt.Println(handle)
		return
	}
	if *transport != "tmux" {
		log.Fatalf("unknown transport %q", *transport)
	}
	if *sandbox != "" || *approval != "" || *metadata != "" {
		log.Fatal("--sandbox, --approval-policy, and --metadata-json require --transport app-server")
	}

	a := adapter.Get(*agentName)
	if a == nil {
		log.Fatalf("unknown agent %q — available: %s", *agentName, strings.Join(adapter.List(), ", "))
	}

	cfg := adapter.Config{
		Model:          *model,
		PermissionMode: *permMode,
		SessionName:    *name,
		ExtraArgs:      []string(agentArgs),
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
	if appserver.IsHandle(sessionName) {
		st, err := appserver.Call(ctx, appServerRoot(), sessionName, appserver.Command{Op: "steer", Text: prompt})
		if err != nil {
			log.Fatalf("steer: %v", err)
		}
		printJSON(st)
		return
	}

	// Resolve the adapter from the session name (zaka-<agent>-<millis>) so
	// prompt formatting and custom submit keys apply. Agent names may
	// themselves contain dashes, so split off the trailing millis segment.
	sess := &tmux.Session{Name: sessionName, Adapter: adapterForSession(sessionName)}

	if err := sess.SendPrompt(ctx, prompt); err != nil {
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

// adapterForSession infers the agent adapter from a zaka session name of the
// form zaka-<agent>-<millis>. Returns nil if the name doesn't match a
// registered adapter.
func adapterForSession(sessionName string) adapter.AgentAdapter {
	rest, ok := strings.CutPrefix(sessionName, "zaka-")
	if !ok {
		return nil
	}
	i := strings.LastIndex(rest, "-")
	if i < 0 {
		return nil
	}
	if a := adapter.Get(rest[:i]); a != nil {
		return a
	}
	return nil
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
	if appserver.IsHandle(args[0]) {
		err := appserver.Kill(ctx, appServerRoot(), args[0])
		if err != nil {
			log.Fatalf("kill: %v", err)
		}
		fmt.Printf("killed %s\n", args[0])
		return
	}
	sess := &tmux.Session{Name: args[0]}
	if err := sess.Kill(ctx); err != nil {
		log.Fatalf("kill: %v", err)
	}
	fmt.Printf("killed %s\n", args[0])
}

func appServerRoot() string {
	root, err := appserver.StateRoot()
	if err != nil {
		log.Fatal(err)
	}
	return root
}

func printJSON(value any) {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		log.Fatal(err)
	}
}

func cmdAppServer(ctx context.Context, op string, args []string) {
	if len(args) == 0 {
		log.Fatalf("usage: zaka %s <session> [flags]", op)
	}
	fs := flag.NewFlagSet(op, flag.ExitOnError)
	jsonOutput := fs.Bool("json", false, "Output structured JSON")
	id := fs.String("request-id", "", "Pending server request id (number or string)")
	answers := fs.String("answers-json", "", "Map question IDs to {\"answers\":[\"text\"]}")
	fs.Parse(args[1:])
	if fs.NArg() != 0 {
		log.Fatal("unexpected positional arguments")
	}
	if !appserver.IsHandle(args[0]) {
		log.Fatal("command requires an App Server session handle")
	}
	var st appserver.Status
	var err error
	if op == "answer" {
		if *id == "" || *answers == "" {
			log.Fatal("answer requires --request-id and --answers-json")
		}
		st, err = appserver.Call(ctx, appServerRoot(), args[0], appserver.Command{Op: "answer", RequestID: *id, Answers: json.RawMessage(*answers)})
	} else {
		st, err = appserver.Inspect(ctx, appServerRoot(), args[0])
	}
	if err != nil {
		log.Fatalf("%s: %v", op, err)
	}
	if *jsonOutput || op == "answer" {
		printJSON(st)
		return
	}
	fmt.Printf("%s phase=%s thread=%s turn=%s pending=%d\nevents: %s\n", st.Session, st.Phase, st.ThreadID, st.TurnID, len(st.Pending), st.EventLog)
	if st.Error != "" {
		fmt.Printf("error: %s\n", st.Error)
	}
	if op == "questions" {
		for _, r := range st.Pending {
			fmt.Printf("%s %s answered=%t %s\n", r.ID, r.Method, r.Answered, r.Params)
		}
	}
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
  spawn    Start an agent (tmux, or Codex app-server)
  steer    Send a prompt to a running session
  list     List active zaka sessions
  kill     Kill a session
  agents   List available agent adapters
  status   Inspect an App Server session (--json)
  questions Show pending structured server requests (--json)
  answer   Answer a pending user-input request

Usage:
  zaka spawn --agent claude-code --workdir .
  zaka steer <session> "fix the auth bug"
  zaka list
  zaka kill <session>
  zaka spawn --agent codex --transport app-server --workdir . --model gpt-6-astra --sandbox workspace-write --approval-policy on-request
  zaka status <session> --json
  zaka questions <session> --json
  zaka answer <session> --request-id 42 --answers-json '{"question_id":{"answers":["text"]}}'
`)
}
