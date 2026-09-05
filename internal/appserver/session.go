package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxMessage = 8 << 20
const userInputMethod = "item/tool/requestUserInput"

type Request struct {
	ID       json.RawMessage `json:"id"`
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params"`
	Answered bool            `json:"answered"`
}
type Status struct {
	Session    string    `json:"session"`
	Transport  string    `json:"transport"`
	ThreadID   string    `json:"thread_id"`
	TurnID     string    `json:"turn_id"`
	TurnStatus string    `json:"turn_status,omitempty"`
	Phase      string    `json:"phase"`
	Error      string    `json:"error,omitempty"`
	Pending    []Request `json:"pending_requests"`
	EventLog   string    `json:"event_log"`
	Config     Config    `json:"config"`
	UpdatedAt  time.Time `json:"updated_at"`
	PID        int       `json:"worker_pid"`
}
type wireMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}
type response struct {
	message wireMessage
	err     error
}
type pendingRPC struct {
	method string
	reply  chan response
}

// The reader owns protocol ordering. mu guards snapshots and outstanding RPCs;
// op serializes mutations across independent CLI processes. Status never needs op.
type Session struct {
	Dir          string
	conn         io.ReadWriteCloser
	log          *os.File
	timeout      time.Duration
	mu           sync.Mutex
	op           chan struct{}
	writer       sync.Mutex
	state        Status
	active       bool
	startingTurn bool
	ended        string
	next         uint64
	calls        map[string]pendingRPC
	requests     map[string]Request
	done         chan struct{}
	once         sync.Once
	readerDone   chan struct{}
}

func NewSession(conn io.ReadWriteCloser, dir, handle string, c Config, timeout time.Duration) (*Session, error) {
	if _, err := c.CommandArgs(); err != nil {
		return nil, err
	}
	if err := privateDir(dir); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	s := &Session{Dir: dir, conn: conn, log: f, timeout: timeout, op: make(chan struct{}, 1), calls: map[string]pendingRPC{}, requests: map[string]Request{}, done: make(chan struct{}), readerDone: make(chan struct{})}
	s.state = Status{Session: handle, Transport: "app-server", Phase: "starting", Pending: []Request{}, EventLog: filepath.Join(dir, "events.jsonl"), Config: c, PID: os.Getpid()}
	if err = s.saveLocked(); err != nil {
		f.Close()
		return nil, err
	}
	go s.read()
	return s, nil
}

func (s *Session) saveLocked() error {
	s.state.Pending = make([]Request, 0, len(s.requests))
	for _, r := range s.requests {
		s.state.Pending = append(s.state.Pending, r)
	}
	sort.Slice(s.state.Pending, func(i, j int) bool { return string(s.state.Pending[i].ID) < string(s.state.Pending[j].ID) })
	s.state.UpdatedAt = time.Now().UTC()
	return writeJSON(filepath.Join(s.Dir, "state.json"), s.state)
}
func (s *Session) phaseLocked() {
	if s.state.Phase == "disconnected" || s.state.Phase == "killed" || s.state.Error != "" {
		return
	}
	if s.active {
		s.state.Phase = "running"
	} else if s.startingTurn {
		s.state.Phase = "starting-turn"
	} else if s.state.TurnID != "" {
		s.state.Phase = "completed"
		if s.state.TurnStatus == "interrupted" {
			s.state.Phase = "interrupted"
		}
	} else if s.state.ThreadID != "" {
		s.state.Phase = "ready"
	}
	for _, r := range s.requests {
		if r.Method != userInputMethod {
			s.state.Phase = "blocked-approval"
			return
		}
		var p struct {
			Blocking bool `json:"isBlocking"`
		}
		json.Unmarshal(r.Params, &p)
		if p.Blocking {
			s.state.Phase = "waiting-input"
		}
	}
}
func (s *Session) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Copy through JSON so caller-owned slices/maps cannot race with the reader.
	b, _ := json.Marshal(s.state)
	var out Status
	json.Unmarshal(b, &out)
	return out
}
func (s *Session) recordLocked(direction string, m wireMessage) error {
	b, err := json.Marshal(map[string]any{"time": time.Now().UTC(), "direction": direction, "message": m})
	if err != nil {
		return err
	}
	if _, err = s.log.Write(append(b, '\n')); err != nil {
		return err
	}
	return s.log.Sync()
}

func (s *Session) fail(err error, phase string) {
	s.once.Do(func() {
		s.mu.Lock()
		s.state.Phase = phase
		s.state.Error = err.Error()
		_ = s.saveLocked()
		for _, p := range s.calls {
			p.reply <- response{err: err}
		}
		s.calls = map[string]pendingRPC{}
		close(s.done)
		s.mu.Unlock()
		s.conn.Close()
	})
}
func (s *Session) Close() {
	s.fail(errors.New("session closed"), "disconnected")
	<-s.readerDone
	s.log.Close()
}
func (s *Session) Kill()                 { s.fail(errors.New("session killed"), "killed") }
func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) send(ctx context.Context, m wireMessage) error {
	s.writer.Lock()
	defer s.writer.Unlock()
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	s.mu.Lock()
	err := s.recordLocked("send", m)
	s.mu.Unlock()
	if err != nil {
		s.fail(err, "error")
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	written := make(chan error, 1)
	go func() { _, err := io.Copy(s.conn, strings.NewReader(string(b))); written <- err }()
	select {
	case err = <-written:
		if err != nil {
			s.fail(err, "disconnected")
		}
		return err
	case <-ctx.Done():
		s.fail(fmt.Errorf("app-server write: %w", ctx.Err()), "disconnected")
		return ctx.Err()
	case <-s.done:
		return errors.New("app-server disconnected")
	}
}
func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func (s *Session) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	s.mu.Lock()
	select {
	case <-s.done:
		s.mu.Unlock()
		return nil, errors.New("app-server disconnected")
	default:
	}
	s.next++
	id := raw(fmt.Sprintf("zaka-%d", s.next))
	ch := make(chan response, 1)
	s.calls[string(id)] = pendingRPC{method, ch}
	s.mu.Unlock()
	if err := s.send(ctx, wireMessage{ID: id, Method: method, Params: raw(params)}); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		if len(r.message.Error) > 0 {
			return nil, fmt.Errorf("%s: %s", method, r.message.Error)
		}
		return r.message.Result, nil
	case <-ctx.Done():
		s.fail(fmt.Errorf("%s outcome unknown: %w", method, ctx.Err()), "disconnected")
		return nil, ctx.Err()
	}
}

func (s *Session) Initialize(ctx context.Context) error {
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer s.release()
	if _, err := s.rpc(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "zaka", "version": "1"}, "capabilities": map[string]any{"experimentalApi": true}}); err != nil {
		return err
	}
	if err := s.send(ctx, wireMessage{Method: "initialized", Params: raw(map[string]any{})}); err != nil {
		return err
	}
	_, err := s.rpc(ctx, "thread/start", s.state.Config.threadParams())
	return err
}

func (s *Session) Steer(ctx context.Context, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("prompt must not be empty")
	}
	if len(text) > maxMessage/2 {
		return errors.New("prompt too large")
	}
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer s.release()
	s.mu.Lock()
	if s.state.Error != "" || s.state.ThreadID == "" {
		s.mu.Unlock()
		return errors.New("session is not steerable; inspect status")
	}
	p := map[string]any{"threadId": s.state.ThreadID, "input": []any{map[string]any{"type": "text", "text": text}}}
	method := "turn/start"
	if s.active {
		method = "turn/steer"
		p["expectedTurnId"] = s.state.TurnID
	} else {
		_, effort, tier, _ := s.state.Config.routing()
		if effort != "" {
			p["effort"] = effort
		}
		if tier != "" {
			p["serviceTier"] = tier
		}
		s.startingTurn = true
		s.phaseLocked()
		if err := s.saveLocked(); err != nil {
			s.mu.Unlock()
			s.fail(err, "error")
			return err
		}
	}
	s.mu.Unlock()
	_, err := s.rpc(ctx, method, p)
	return err
}

func requestMatches(rawID json.RawMessage, id string) bool {
	if string(rawID) == id {
		return true
	}
	var v string
	return json.Unmarshal(rawID, &v) == nil && v == id
}
func validateAnswers(params, answers json.RawMessage) error {
	var p struct {
		Questions []struct {
			ID string `json:"id"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return err
	}
	var a map[string]struct {
		Answers []string `json:"answers"`
	}
	d := json.NewDecoder(strings.NewReader(string(answers)))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return err
	}
	if !json.Valid(answers) || a == nil || len(a) != len(p.Questions) || len(a) == 0 {
		return errors.New("answers must map every question id to {\"answers\":[\"text\"]}")
	}
	for _, q := range p.Questions {
		v, ok := a[q.ID]
		if !ok || v.Answers == nil {
			return fmt.Errorf("missing answers for question %q", q.ID)
		}
	}
	return nil
}
func (s *Session) Answer(ctx context.Context, id string, answers json.RawMessage) error {
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer s.release()
	s.mu.Lock()
	select {
	case <-s.done:
		s.mu.Unlock()
		return errors.New("app-server disconnected")
	default:
	}
	var key string
	var r Request
	for k, v := range s.requests {
		if requestMatches(v.ID, id) {
			if key != "" {
				s.mu.Unlock()
				return errors.New("ambiguous request id; use its JSON string form")
			}
			key = k
			r = v
		}
	}
	if key == "" {
		s.mu.Unlock()
		return errors.New("request id is not pending")
	}
	if r.Method != userInputMethod {
		s.mu.Unlock()
		return fmt.Errorf("request kind %s cannot be answered; approval remains parked", r.Method)
	}
	if r.Answered {
		s.mu.Unlock()
		return errors.New("request already answered; waiting for serverRequest/resolved")
	}
	if err := validateAnswers(r.Params, answers); err != nil {
		s.mu.Unlock()
		return err
	}
	// Reserve under the same lock used by serverRequest/resolved. A concurrent
	// resolution may remove it, but can never resurrect an answered request.
	r.Answered = true
	s.requests[key] = r
	err := s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		s.fail(err, "error")
		return err
	}
	return s.send(ctx, wireMessage{ID: r.ID, Result: raw(map[string]any{"answers": answers})})
}

func (s *Session) read() {
	defer close(s.readerDone)
	scan := bufio.NewScanner(s.conn)
	scan.Buffer(make([]byte, 65536), maxMessage)
	for scan.Scan() {
		var m wireMessage
		if err := json.Unmarshal(scan.Bytes(), &m); err != nil {
			s.fail(fmt.Errorf("invalid app-server JSON: %w", err), "disconnected")
			return
		}
		s.mu.Lock()
		err := s.recordLocked("receive", m)
		if err == nil {
			err = s.applyLocked(m)
		}
		if err == nil {
			err = s.saveLocked()
		}
		s.mu.Unlock()
		if err != nil {
			s.fail(err, "disconnected")
			return
		}
	}
	err := scan.Err()
	if err == nil {
		err = io.EOF
	}
	s.fail(fmt.Errorf("app-server disconnected: %w", err), "disconnected")
}

func (s *Session) applyLocked(m wireMessage) error {
	if m.Method == "" {
		p, ok := s.calls[string(m.ID)]
		if !ok {
			return nil
		}
		delete(s.calls, string(m.ID))
		if p.method == "turn/start" {
			s.startingTurn = false
		}
		var err error
		if len(m.Error) > 0 && string(m.Error) != "null" {
			s.state.Error = fmt.Sprintf("%s: %s", p.method, m.Error)
			s.state.Phase = "error"
		} else {
			switch p.method {
			case "thread/start":
				var result struct {
					Thread struct {
						ID string `json:"id"`
					} `json:"thread"`
				}
				json.Unmarshal(m.Result, &result)
				if result.Thread.ID == "" {
					err = errors.New("thread/start response missing thread id")
				} else {
					s.state.ThreadID = result.Thread.ID
					err = s.state.Config.verifyThread(m.Result)
				}
			case "turn/start":
				var result struct {
					Turn struct {
						ID     string          `json:"id"`
						Status string          `json:"status"`
						Error  json.RawMessage `json:"error"`
					} `json:"turn"`
				}
				json.Unmarshal(m.Result, &result)
				if result.Turn.ID == "" {
					err = errors.New("turn/start response missing turn id")
				} else if s.ended != result.Turn.ID {
					s.state.TurnID = result.Turn.ID
					s.setTurnLocked(result.Turn.Status, result.Turn.Error)
				}
			}
		}
		s.phaseLocked()
		// Persist before waking the caller: returning from spawn or steer is a
		// durable acknowledgement, never a claim that generation completed.
		if err == nil {
			err = s.saveLocked()
		}
		p.reply <- response{message: m, err: err}
		return err
	}
	if len(m.ID) > 0 && string(m.ID) != "null" {
		if _, exists := s.requests[string(m.ID)]; exists {
			return fmt.Errorf("duplicate server request id %s", m.ID)
		}
		s.requests[string(m.ID)] = Request{ID: m.ID, Method: m.Method, Params: m.Params}
		s.phaseLocked()
		return nil
	}
	var p struct {
		ThreadID  string          `json:"threadId"`
		RequestID json.RawMessage `json:"requestId"`
		Turn      struct {
			ID     string          `json:"id"`
			Status string          `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"turn"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return nil
	}
	if p.ThreadID != "" && s.state.ThreadID != "" && p.ThreadID != s.state.ThreadID {
		return nil
	}
	switch m.Method {
	case "serverRequest/resolved":
		delete(s.requests, string(p.RequestID))
	case "turn/started":
		if p.Turn.ID != "" && s.ended != p.Turn.ID {
			s.state.TurnID = p.Turn.ID
			s.active = true
			s.startingTurn = false
			s.state.TurnStatus = "inProgress"
		}
	case "turn/completed":
		if p.Turn.ID != "" && p.Turn.ID != s.ended && (s.startingTurn || s.state.TurnID == "" || s.state.TurnID == p.Turn.ID) {
			s.state.TurnID = p.Turn.ID
			s.ended = p.Turn.ID
			s.setTurnLocked(p.Turn.Status, p.Turn.Error)
		}
	case "error":
		s.state.Error = string(p.Error)
		if s.state.Error == "" {
			s.state.Error = "app-server error"
		}
		s.state.Phase = "error"
	}
	s.phaseLocked()
	return nil
}

func (s *Session) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.op <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return errors.New("session disconnected")
	}
}
func (s *Session) release() { <-s.op }

func (s *Session) setTurnLocked(status string, turnError json.RawMessage) {
	s.startingTurn = false
	s.state.TurnStatus = status
	s.active = status == "inProgress"
	if !s.active {
		s.ended = s.state.TurnID
	}
	if status == "failed" {
		s.state.Error = string(turnError)
		if s.state.Error == "" || s.state.Error == "null" {
			s.state.Error = "turn failed"
		}
		s.state.Phase = "error"
	}
}
