package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCLIAppServer(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "zcli-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	bin := filepath.Join(root, "zaka")
	build := exec.Command("go", "build", "-o", bin, ".")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", b, err)
	}
	// Shell speaks the minimal no-inference protocol for CLI persistence tests.
	script := `#!/bin/sh
while IFS= read -r line; do
 case "$line" in
 *'"method":"initialize"'*) printf '%s\n' '{"id":"zaka-1","result":{"userAgent":"mock"}}' ;;
 *'"method":"thread/start"'*) printf '%s\n' '{"id":"zaka-2","result":{"thread":{"id":"cli-thread"},"model":"gpt-6-astra","cwd":"` + root + `","sandbox":{"type":"readOnly"},"approvalPolicy":"on-request","approvalsReviewer":"user","reasoningEffort":"xhigh"}}' ;;
 *'"method":"turn/start"'*) printf '%s\n' '{"id":"zaka-3","result":{"turn":{"id":"cli-turn","status":"inProgress"}}}' '{"id":9,"method":"item/tool/requestUserInput","params":{"threadId":"cli-thread","turnId":"cli-turn","itemId":"i","isBlocking":true,"questions":[{"id":"q","header":"Q","question":"Which?"}]}}' ;;
 *'"result"'*) printf '%s\n' '{"method":"serverRequest/resolved","params":{"threadId":"cli-thread","requestId":9}}' ;;
 esac
done
`
	if err := os.WriteFile(filepath.Join(root, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + root + ":/usr/bin:/bin", "HOME=" + root, "CODEX_HOME=" + root, "ZAKA_STATE_DIR=" + filepath.Join(root, "state")}
	run := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = env
		b, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(b)), err
	}
	base := []string{"spawn", "--agent", "codex", "--transport", "app-server", "--workdir", root, "--model", "gpt-6-astra", "--sandbox", "read-only", "--approval-policy", "on-request", "--metadata-json", `{"route":"opaque"}`, "--agent-arg=-c", "--agent-arg=model_reasoning_effort=xhigh"}
	handle, err := run(base...)
	if err != nil {
		t.Fatalf("spawn: %s %v", handle, err)
	}
	defer run("kill", handle)
	if !strings.HasPrefix(handle, "as-") {
		t.Fatalf("not a generated handle: %s", handle)
	}
	out, err := run("status", handle, "--json")
	if err != nil {
		t.Fatalf("status: %s %v", out, err)
	}
	var st map[string]any
	if err = json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatal(err)
	}
	if st["phase"] != "ready" || st["thread_id"] != "cli-thread" {
		t.Fatalf("wrong ready state %s", out)
	}
	if out, err = run("steer", handle, "ask me"); err != nil {
		t.Fatalf("steer: %s %v", out, err)
	}
	out, err = run("questions", handle, "--json")
	if err != nil || !strings.Contains(out, `"questions"`) {
		t.Fatalf("questions: %s %v", out, err)
	}
	out, err = run("answer", handle, "--request-id", "9", "--answers-json", `{"q":{"answers":["yes"]}}`)
	if err != nil {
		t.Fatalf("answer: %s %v", out, err)
	}
	out, err = run("kill", handle)
	if err != nil {
		t.Fatalf("kill: %s %v", out, err)
	}
	out, err = run("spawn", "--agent", "gemini", "--transport", "app-server")
	if err == nil {
		t.Fatalf("non-Codex accepted: %s", out)
	}
	out, err = run("spawn", "--transport", "typo")
	if err == nil {
		t.Fatalf("transport typo accepted: %s", out)
	}
}

func TestAgentArgsAreRepeatableAndOrdered(t *testing.T) {
	fs := flag.NewFlagSet("spawn", flag.ContinueOnError)
	var args stringListFlag
	fs.Var(&args, "agent-arg", "")
	if err := fs.Parse([]string{
		"--agent-arg", "-c",
		"--agent-arg", "model_reasoning_effort=high",
		"--agent-arg", "-c",
		"--agent-arg", "service_tier=default",
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"-c", "model_reasoning_effort=high", "-c", "service_tier=default"}
	if !reflect.DeepEqual([]string(args), want) {
		t.Fatalf("agent args = %q, want %q", args, want)
	}
}
