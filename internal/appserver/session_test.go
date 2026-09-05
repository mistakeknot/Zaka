package appserver

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func config(t *testing.T) Config {
	t.Helper()
	return Config{WorkDir: t.TempDir(), Model: "gpt-6-astra", Sandbox: "workspace-write", ApprovalPolicy: "on-request", AgentArgs: []string{"-c", "model_reasoning_effort=xhigh", "-c", "service_tier=Standard"}, Metadata: json.RawMessage(`{"route":{"id":17}}`)}
}

func TestConfigSafety(t *testing.T) {
	c := config(t)
	args, err := c.CommandArgs()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"app-server", "--enable", "default_mode_request_user_input", "-c", "model_reasoning_effort=xhigh", "-c", "service_tier=default"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args: %v", args)
	}
	for _, extra := range [][]string{{"--dangerously-bypass-approvals-and-sandbox"}, {"-c", "sandbox_mode=\"danger-full-access\""}, {"-c", "approval_policy=\"never\""}, {"-c", "model=\"other\""}, {"-c", "unknown=1"}} {
		bad := c
		bad.AgentArgs = extra
		if _, err := bad.CommandArgs(); err == nil {
			t.Errorf("accepted unsafe/unsupported args %v", extra)
		}
	}
	for _, policy := range []string{"", "on-failure", "bogus"} {
		bad := c
		bad.ApprovalPolicy = policy
		if _, err := bad.CommandArgs(); err == nil {
			t.Errorf("accepted policy %q", policy)
		}
	}
	c.Sandbox = "danger-full-access"
	if _, err := c.CommandArgs(); err == nil {
		t.Fatal("accepted unsafe sandbox")
	}
}

type mockPeer struct {
	c net.Conn
	d *json.Decoder
	e *json.Encoder
	t *testing.T
}

func (p *mockPeer) receive(method string) map[string]json.RawMessage {
	p.t.Helper()
	p.c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var m map[string]json.RawMessage
	if err := p.d.Decode(&m); err != nil {
		p.t.Fatal(err)
	}
	var got string
	json.Unmarshal(m["method"], &got)
	if got != method {
		p.t.Fatalf("method %q, want %q; %s", got, method, m)
	}
	return m
}
func (p *mockPeer) send(v any) {
	p.t.Helper()
	p.c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := p.e.Encode(v); err != nil {
		p.t.Fatal(err)
	}
}
func (p *mockPeer) reply(m map[string]json.RawMessage, result any) {
	p.send(map[string]any{"id": m["id"], "result": result})
}
func newSession(t *testing.T) (*Session, *mockPeer) {
	t.Helper()
	a, b := net.Pipe()
	dir := filepath.Join(t.TempDir(), "private")
	c := config(t)
	s, err := NewSession(a, dir, "as-test", c, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); b.Close() })
	return s, &mockPeer{b, json.NewDecoder(b), json.NewEncoder(b), t}
}
func initialize(t *testing.T, s *Session, p *mockPeer) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- s.Initialize(context.Background()) }()
	m := p.receive("initialize")
	var init struct {
		Capabilities struct {
			Experimental bool `json:"experimentalApi"`
		}
	}
	json.Unmarshal(m["params"], &init)
	if !init.Capabilities.Experimental {
		t.Fatal("user-input experimental capability missing")
	}
	p.reply(m, map[string]any{"userAgent": "mock"})
	p.receive("initialized")
	m = p.receive("thread/start")
	var params map[string]any
	json.Unmarshal(m["params"], &params)
	if params["sandbox"] != "workspace-write" || params["approvalPolicy"] != "on-request" || params["model"] != "gpt-6-astra" || params["serviceTier"] != "default" || params["approvalsReviewer"] != "user" {
		t.Fatalf("thread params %v", params)
	}
	p.reply(m, threadReply(s.Status().Config, "thread-1"))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.Status().Phase != "ready" {
		t.Fatalf("spawn is not completion: %+v", s.Status())
	}
}

func threadReply(c Config, id string) map[string]any {
	_, effort, tier, _ := c.routing()
	sandbox := "workspaceWrite"
	if c.Sandbox == "read-only" {
		sandbox = "readOnly"
	}
	return map[string]any{"thread": map[string]any{"id": id}, "model": c.Model, "cwd": c.WorkDir, "approvalPolicy": c.ApprovalPolicy, "approvalsReviewer": "user", "sandbox": map[string]any{"type": sandbox}, "reasoningEffort": effort, "serviceTier": tier}
}

func TestRejectEffectiveRouteDrift(t *testing.T) {
	for _, field := range []string{"model", "approvalPolicy", "approvalsReviewer", "sandbox", "reasoningEffort", "serviceTier", "cwd"} {
		t.Run(field, func(t *testing.T) {
			s, p := newSession(t)
			done := make(chan error, 1)
			go func() { done <- s.Initialize(context.Background()) }()
			m := p.receive("initialize")
			p.reply(m, map[string]any{})
			p.receive("initialized")
			m = p.receive("thread/start")
			result := threadReply(s.Status().Config, "thread-1")
			result[field] = "unexpected"
			p.reply(m, result)
			if err := <-done; err == nil {
				t.Fatalf("accepted changed %s", field)
			}
		})
	}
}

func TestTerminalTurnBeforeReply(t *testing.T) {
	for _, status := range []string{"completed", "interrupted", "failed"} {
		t.Run(status, func(t *testing.T) {
			s, p := newSession(t)
			initialize(t, s, p)
			steer(t, s, p, false)
			p.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-1", "turn": map[string]any{"id": "turn-1", "status": "completed"}}})
			await(t, s, func(st Status) bool { return st.Phase == "completed" })
			done := make(chan error, 1)
			go func() { done <- s.Steer(context.Background(), "next") }()
			m := p.receive("turn/start")
			if st := s.Status(); st.Phase != "starting-turn" {
				t.Fatalf("next turn falsely appears finished: %+v", st)
			}
			turn := map[string]any{"id": "turn-2", "status": status, "error": map[string]any{"message": "failed"}}
			p.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-1", "turn": turn}})
			p.reply(m, map[string]any{"turn": map[string]any{"id": "turn-2", "status": "inProgress"}})
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			want := status
			if status == "failed" {
				want = "error"
			}
			if st := s.Status(); st.Phase != want || st.TurnID != "turn-2" {
				t.Fatalf("terminal event overwritten: %+v", st)
			}
		})
	}
}

func TestCanceledMutationDoesNotWaitForPreviousRPC(t *testing.T) {
	s, p := newSession(t)
	initialize(t, s, p)
	first := make(chan error, 1)
	go func() { first <- s.Steer(context.Background(), "first") }()
	m := p.receive("turn/start")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	second := make(chan error, 1)
	go func() { second <- s.Steer(ctx, "second") }()
	select {
	case err := <-second:
		if err == nil {
			t.Fatal("canceled call accepted")
		}
	case <-time.After(50 * time.Millisecond):
		t.Error("canceled call blocked behind RPC")
	}
	p.reply(m, map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}})
	<-first
}

func TestModelAdvertisedEffortIsPreserved(t *testing.T) {
	c := config(t)
	c.AgentArgs = []string{"-c", "model_reasoning_effort=future-effort"}
	if _, err := c.CommandArgs(); err != nil {
		t.Fatalf("version-specific schema accepts any nonempty effort: %v", err)
	}
}
func steer(t *testing.T, s *Session, p *mockPeer, active bool) {
	t.Helper()
	turnID := "turn-1"
	if !active && s.Status().TurnID == "turn-1" {
		turnID = "turn-2"
	}
	done := make(chan error, 1)
	go func() { done <- s.Steer(context.Background(), "do work") }()
	method := "turn/start"
	if active {
		method = "turn/steer"
	}
	m := p.receive(method)
	var params map[string]any
	json.Unmarshal(m["params"], &params)
	if params["threadId"] != "thread-1" {
		t.Fatalf("thread missing %v", params)
	}
	if active {
		if params["expectedTurnId"] != "turn-1" {
			t.Fatalf("wrong turn %v", params)
		}
		p.reply(m, map[string]any{"turnId": "turn-1"})
	} else {
		if params["effort"] != "xhigh" || params["serviceTier"] != "default" {
			t.Fatalf("lost route %v", params)
		}
		p.reply(m, map[string]any{"turn": map[string]any{"id": turnID, "status": "inProgress"}})
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func await(t *testing.T, s *Session, predicate func(Status) bool) Status {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		st := s.Status()
		if predicate(st) {
			return st
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out: %+v", st)
		case <-tick.C:
		}
	}
}
func TestProtocolLifecycleAndQuestions(t *testing.T) {
	s, p := newSession(t)
	initialize(t, s, p)
	steer(t, s, p, false)
	steer(t, s, p, true)
	p.send(map[string]any{"id": 42, "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "itemId": "item-1", "isBlocking": true, "questions": []any{map[string]any{"id": "choice", "header": "Pick", "question": "Which?", "options": nil}}}})
	await(t, s, func(st Status) bool { return st.Phase == "waiting-input" })
	if err := s.Answer(context.Background(), "42", json.RawMessage(`{"bad":{"answers":["x"]}}`)); err == nil {
		t.Fatal("accepted unknown question")
	}
	done := make(chan error, 1)
	go func() {
		done <- s.Answer(context.Background(), "42", json.RawMessage(`{"choice":{"answers":["free text"]}}`))
	}()
	m := p.receive("")
	if string(m["id"]) != "42" || string(m["result"]) != `{"answers":{"choice":{"answers":["free text"]}}}` {
		t.Fatalf("bad answer %s", m)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(s.Status().Pending) != 1 || !s.Status().Pending[0].Answered {
		t.Fatal("must retain request until resolved")
	}
	if err := s.Answer(context.Background(), "42", json.RawMessage(`{"choice":{"answers":["again"]}}`)); err == nil {
		t.Fatal("duplicate answer accepted")
	}
	p.send(map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"threadId": "thread-1", "requestId": 42}})
	await(t, s, func(st Status) bool { return len(st.Pending) == 0 })
	p.send(map[string]any{"id": "approval-1", "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "command": "danger"}})
	await(t, s, func(st Status) bool { return st.Phase == "blocked-approval" })
	if err := s.Answer(context.Background(), "approval-1", json.RawMessage(`{}`)); err == nil {
		t.Fatal("approved non-question")
	}
	p.send(map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"threadId": "thread-1", "requestId": "approval-1"}})
	p.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-1", "turn": map[string]any{"id": "turn-1", "status": "completed"}}})
	st := await(t, s, func(st Status) bool { return st.Phase == "completed" })
	if st.TurnID != "turn-1" {
		t.Fatal("lost final turn")
	}
	steer(t, s, p, false)
	if s.Status().TurnID != "turn-2" || s.Status().Phase != "running" {
		t.Fatal("next turn not active")
	}
	for _, name := range []string{"state.json", "events.jsonl"} {
		info, err := os.Stat(filepath.Join(s.Dir, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private %s: %v %v", name, info, err)
		}
	}
	if !reflect.DeepEqual(s.Status().Config.Metadata, json.RawMessage(`{"route":{"id":17}}`)) {
		t.Fatal("metadata altered")
	}
	p.c.Close()
	await(t, s, func(st Status) bool { return st.Phase == "disconnected" })
	if err := s.Steer(context.Background(), "retry"); err == nil {
		t.Fatal("steered dead process")
	}
}

func TestNonblockingQuestionAndRemoteResolution(t *testing.T) {
	s, p := newSession(t)
	initialize(t, s, p)
	steer(t, s, p, false)
	p.send(map[string]any{"id": "async", "method": userInputMethod, "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "itemId": "i", "isBlocking": false, "questions": []any{map[string]any{"id": "q", "question": "Next?", "header": "Next"}}}})
	st := await(t, s, func(st Status) bool { return len(st.Pending) == 1 })
	if st.Phase != "running" {
		t.Fatalf("async question blocked turn: %+v", st)
	}
	p.send(map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"threadId": "thread-1", "requestId": "async"}})
	await(t, s, func(st Status) bool { return len(st.Pending) == 0 })
	if err := s.Answer(context.Background(), "async", json.RawMessage(`{"q":{"answers":["late"]}}`)); err == nil {
		t.Fatal("answered remotely resolved request")
	}
}

func TestProtocolFailureAndDeadReader(t *testing.T) {
	s, p := newSession(t)
	initialize(t, s, p)
	p.c.Write([]byte("invalid JSON\n"))
	st := await(t, s, func(st Status) bool { return st.Phase == "disconnected" })
	if st.Error == "" {
		t.Fatal("missing protocol error")
	}
}

func TestPrivateStateRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := privateDir(link); err == nil {
		t.Fatal("symlink state directory accepted")
	}
}

func TestPolicyErrorDoesNotRetryOrFallback(t *testing.T) {
	s, p := newSession(t)
	initialize(t, s, p)
	done := make(chan error, 1)
	go func() { done <- s.Steer(context.Background(), "work") }()
	m := p.receive("turn/start")
	p.send(map[string]any{"id": m["id"], "error": map[string]any{"code": -1, "message": "policy blocked"}})
	if err := <-done; err == nil {
		t.Fatal("error swallowed")
	}
	if s.Status().Phase != "error" {
		t.Fatalf("not visible %+v", s.Status())
	}
	if err := s.Steer(context.Background(), "retry"); err == nil {
		t.Fatal("policy block retried")
	}
}

func TestReadinessTimeout(t *testing.T) {
	s, p := newSession(t)
	defer p.c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := s.Initialize(ctx); err == nil {
		t.Fatal("silent peer accepted")
	}
	if time.Since(start) > time.Second {
		t.Fatal("unbounded readiness")
	}
}
