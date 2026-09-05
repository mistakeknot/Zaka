package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Re-exec the test binary as both worker and Codex. No live account, API,
// config, or credentials are used by this transport integration fixture.
func TestWorkerHelper(t *testing.T) {
	mode := os.Getenv("ZAKA_TEST_HELPER")
	if mode == "" {
		return
	}
	if mode == "worker" {
		root, handle := os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]
		f := os.NewFile(3, "ready")
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		err := RunWorker(ctx, root, handle, f)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if mode != "server" {
		return
	}
	d := json.NewDecoder(os.Stdin)
	e := json.NewEncoder(os.Stdout)
	initialized := false
	turn := 0
	for {
		var m wireMessage
		if d.Decode(&m) != nil {
			os.Exit(0)
		}
		switch m.Method {
		case "initialize":
			e.Encode(map[string]any{"id": m.ID, "result": map[string]any{"userAgent": "mock"}})
		case "initialized":
			initialized = true
		case "thread/start":
			if !initialized {
				os.Exit(3)
			}
			var params struct {
				Model    string            `json:"model"`
				Cwd      string            `json:"cwd"`
				Sandbox  string            `json:"sandbox"`
				Approval string            `json:"approvalPolicy"`
				Tier     string            `json:"serviceTier"`
				Config   map[string]string `json:"config"`
			}
			json.Unmarshal(m.Params, &params)
			c := Config{Model: params.Model, WorkDir: params.Cwd, Sandbox: params.Sandbox, ApprovalPolicy: params.Approval, AgentArgs: []string{"-c", "model_reasoning_effort=" + params.Config["model_reasoning_effort"], "-c", "service_tier=" + params.Tier}}
			e.Encode(map[string]any{"id": m.ID, "result": threadReply(c, "persistent-thread")})
		case "turn/start":
			if strings.Contains(string(m.Params), "exit-process") {
				os.Exit(23)
			}
			turn++
			id := fmt.Sprintf("t-%d", turn)
			e.Encode(map[string]any{"id": m.ID, "result": map[string]any{"turn": map[string]any{"id": id, "status": "inProgress"}}})
			e.Encode(map[string]any{"id": "input-1", "method": userInputMethod, "params": map[string]any{"threadId": "persistent-thread", "turnId": id, "itemId": "i-1", "isBlocking": true, "questions": []any{map[string]any{"id": "q", "header": "Q", "question": "Continue?"}}}})
		case "turn/steer":
			e.Encode(map[string]any{"id": m.ID, "result": map[string]any{"turnId": fmt.Sprintf("t-%d", turn)}})
		case "":
			e.Encode(map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"threadId": "persistent-thread", "requestId": m.ID}})
			e.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "persistent-thread", "turn": map[string]any{"id": fmt.Sprintf("t-%d", turn), "status": "completed"}}})
		}
	}
}

func TestDurableWorker(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "zk-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	bin := filepath.Join(root, "codex")
	script := "#!/bin/sh\nZAKA_TEST_HELPER=server exec '" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "' -test.run=TestWorkerHelper\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+":"+os.Getenv("PATH"))
	t.Setenv("ZAKA_TEST_HELPER", "worker")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	handle, err := spawn(ctx, root, config(t), []string{os.Args[0], "-test.run=TestWorkerHelper", "--"})
	if err != nil {
		t.Fatal(err)
	}
	defer Call(context.Background(), root, handle, Command{Op: "kill"})
	st, err := Inspect(ctx, root, handle)
	if err != nil || st.Phase != "ready" || st.ThreadID != "persistent-thread" {
		t.Fatalf("spawn: %+v %v", st, err)
	}
	st, err = Call(ctx, root, handle, Command{Op: "steer", Text: "ask"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(st.Pending) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		st, err = Inspect(ctx, root, handle)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(st.Pending) != 1 {
		t.Fatalf("missing durable question %+v", st)
	}
	disk, err := readStatus(root, handle)
	if err != nil || len(disk.Pending) != 1 {
		t.Fatalf("disk: %+v %v", disk, err)
	}
	_, err = Call(ctx, root, handle, Command{Op: "answer", RequestID: "input-1", Answers: json.RawMessage(`{"q":{"answers":["yes"]}}`)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Call(ctx, root, handle, Command{Op: "kill"})
	if err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = os.Stat(socketPath(root, handle)); os.IsNotExist(err) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !os.IsNotExist(err) {
		t.Fatal("socket not cleaned up")
	}
	st, err = Inspect(ctx, root, handle)
	if err != nil || st.Phase != "killed" {
		t.Fatalf("kill lost state %+v %v", st, err)
	}
	if _, err = Call(ctx, root, handle, Command{Op: "steer", Text: "again"}); err == nil {
		t.Fatal("dead worker accepted steer")
	}
}

func TestStaleSessionAndPathSafety(t *testing.T) {
	root := t.TempDir()
	handle := "as-00112233445566778899aabb"
	if err := os.Mkdir(filepath.Join(root, handle), 0700); err != nil {
		t.Fatal(err)
	}
	st := Status{Session: handle, Phase: "running", ThreadID: "old-thread", Pending: []Request{{ID: json.RawMessage(`7`), Method: userInputMethod}}, PID: os.Getpid()}
	if err := writeJSON(filepath.Join(root, handle, "state.json"), st); err != nil {
		t.Fatal(err)
	}
	got, err := Inspect(context.Background(), root, handle)
	if err != nil || got.Phase != "stale" || len(got.Pending) != 1 {
		t.Fatalf("stale %+v %v", got, err)
	}
	for _, bad := range []string{"../escape", "as-../../escape", "", "as-nope"} {
		if _, err := Inspect(context.Background(), root, bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestWorkerProcessDeathAndStaleKill(t *testing.T) {
	for _, crash := range []string{"server", "worker"} {
		t.Run(crash, func(t *testing.T) {
			root, err := os.MkdirTemp("/tmp", "zk-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(root)
			script := "#!/bin/sh\nZAKA_TEST_HELPER=server exec '" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "' -test.run=TestWorkerHelper\n"
			if err := os.WriteFile(filepath.Join(root, "codex"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", root+":"+os.Getenv("PATH"))
			t.Setenv("ZAKA_TEST_HELPER", "worker")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			handle, err := spawn(ctx, root, config(t), []string{os.Args[0], "-test.run=TestWorkerHelper", "--"})
			if err != nil {
				t.Fatal(err)
			}
			st, err := Inspect(ctx, root, handle)
			if err != nil {
				t.Fatal(err)
			}
			if crash == "worker" {
				if err := syscall.Kill(st.PID, syscall.SIGKILL); err != nil {
					t.Fatal(err)
				}
			} else {
				_, _ = Call(ctx, root, handle, Command{Op: "steer", Text: "exit-process"})
			}
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				st, err = Inspect(ctx, root, handle)
				if err != nil {
					t.Fatal(err)
				}
				if st.Phase == "stale" || st.Phase == "disconnected" {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if st.Phase != "stale" && st.Phase != "disconnected" {
				t.Fatalf("death not detected %+v", st)
			}
			if err := Kill(ctx, root, handle); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(socketPath(root, handle)); !os.IsNotExist(err) {
				t.Fatal("stale socket not removed")
			}
		})
	}
}

func TestWorkerStartupFailureIsBounded(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "zk-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	if err := os.WriteFile(filepath.Join(root, "codex"), []byte("#!/bin/sh\nexec sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+":/usr/bin:/bin")
	t.Setenv("ZAKA_TEST_HELPER", "worker")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = spawn(ctx, root, config(t), []string{os.Args[0], "-test.run=TestWorkerHelper", "--"})
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("startup not bounded: %v %s", err, time.Since(start))
	}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if IsHandle(entry.Name()) {
			st, err := readStatus(root, entry.Name())
			if err != nil || st.Phase != "error" {
				t.Fatalf("startup failure not durable %+v %v", st, err)
			}
			if _, err := os.Stat(socketPath(root, entry.Name())); !os.IsNotExist(err) {
				t.Fatal("startup socket leaked")
			}
		}
	}
}
