package appserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"
)

const operationTimeout = 8 * time.Second
const startupTimeout = 20 * time.Second

var handlePattern = regexp.MustCompile(`^as-[0-9a-f]{24}$`)

type Command struct {
	Op        string          `json:"op"`
	Text      string          `json:"text,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
	Answers   json.RawMessage `json:"answers,omitempty"`
}
type reply struct {
	Status Status `json:"status"`
	Error  string `json:"error,omitempty"`
}

// StateRoot is separate from Codex configuration and contains private Zaka
// session state only. ZAKA_STATE_DIR is useful for isolated clients and tests.
func StateRoot() (string, error) {
	if root := os.Getenv("ZAKA_STATE_DIR"); root != "" {
		return filepath.Abs(root)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "zaka"), nil
}
func IsHandle(handle string) bool           { return handlePattern.MatchString(handle) }
func socketPath(root, handle string) string { return filepath.Join(root, handle+".sock") }
func sessionPath(root, handle string) (string, error) {
	if !IsHandle(handle) {
		return "", errors.New("invalid app-server session handle")
	}
	path := filepath.Join(root, handle)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("session directory is not private")
	}
	return path, nil
}

func Spawn(ctx context.Context, root string, c Config) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return spawn(ctx, root, c, []string{exe, "_app-server-worker"})
}

func spawn(ctx context.Context, root string, c Config, worker []string) (string, error) {
	if _, err := c.CommandArgs(); err != nil {
		return "", err
	}
	var err error
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	c.WorkDir, err = filepath.Abs(c.WorkDir)
	if err != nil {
		return "", err
	}
	if err = privateDir(root); err != nil {
		return "", err
	}
	b := make([]byte, 12)
	if _, err = rand.Read(b); err != nil {
		return "", err
	}
	handle := "as-" + hex.EncodeToString(b)
	if len(socketPath(root, handle)) > 100 {
		return "", errors.New("state socket path too long; set ZAKA_STATE_DIR to a shorter private path")
	}
	dir := filepath.Join(root, handle)
	if err = os.Mkdir(dir, 0700); err != nil {
		return "", err
	}
	if err = writeJSON(filepath.Join(dir, "config.json"), c); err != nil {
		return "", err
	}
	if err = writeJSON(filepath.Join(dir, "state.json"), Status{Session: handle, Transport: "app-server", Phase: "starting", Config: c, Pending: []Request{}, EventLog: filepath.Join(dir, "events.jsonl"), UpdatedAt: time.Now().UTC()}); err != nil {
		return "", err
	}
	log, err := os.OpenFile(filepath.Join(dir, "worker.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	defer log.Close()
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	defer r.Close()
	args := append(append([]string{}, worker[1:]...), root, handle)
	cmd := exec.Command(worker[0], args...)
	cmd.ExtraFiles = []*os.File{w}
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		w.Close()
		return "", err
	}
	w.Close()
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	ready := make(chan reply, 1)
	go func() {
		var out reply
		if err := json.NewDecoder(io.LimitReader(r, maxMessage)).Decode(&out); err != nil {
			out.Error = fmt.Sprintf("worker readiness: %v", err)
		}
		ready <- out
	}()
	ctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	var out reply
	select {
	case out = <-ready:
		if out.Error == "" && out.Status.Phase == "ready" && out.Status.ThreadID != "" {
			return handle, nil
		}
		if out.Error == "" {
			out.Error = "worker did not become ready"
		}
	case <-ctx.Done():
		out.Error = fmt.Sprintf("worker startup: %v", ctx.Err())
	}
	// This is our newly spawned child, not an untrusted PID from stale disk state.
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-wait:
	case <-time.After(time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-wait
	}
	_ = os.Remove(socketPath(root, handle))
	st, _ := readStatus(root, handle)
	st.Phase = "error"
	st.Error = out.Error
	_ = writeJSON(filepath.Join(dir, "state.json"), st)
	return "", fmt.Errorf("%s (session %s; logs %s)", out.Error, handle, dir)
}

type pipeTransport struct {
	in  *os.File
	out *os.File
}

func (p *pipeTransport) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *pipeTransport) Write(b []byte) (int, error) { return p.in.Write(b) }
func (p *pipeTransport) Close() error                { p.in.Close(); return p.out.Close() }

// RunWorker is an internal re-exec entry point, not a model call by itself.
// The worker outlives the spawn CLI and owns both the Codex process and socket.
func RunWorker(ctx context.Context, root, handle string, ready io.WriteCloser) (err error) {
	announced := false
	defer func() {
		if !announced {
			msg := "worker stopped before ready"
			if err != nil {
				msg = err.Error()
			}
			_ = json.NewEncoder(ready).Encode(reply{Error: msg})
		}
		ready.Close()
	}()
	dir, err := sessionPath(root, handle)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return err
	}
	var c Config
	if err = json.Unmarshal(b, &c); err != nil {
		return err
	}
	args, err := c.CommandArgs()
	if err != nil {
		return err
	}
	// Never remove an existing socket: another worker may own this handle.
	l, err := net.Listen("unix", socketPath(root, handle))
	if err != nil {
		return err
	}
	defer os.Remove(socketPath(root, handle))
	defer l.Close()
	if err = os.Chmod(socketPath(root, handle), 0600); err != nil {
		return err
	}
	stderr, err := os.OpenFile(filepath.Join(dir, "server.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer stderr.Close()
	inR, inW, err := os.Pipe()
	if err != nil {
		return err
	}
	defer inR.Close()
	defer inW.Close()
	outR, outW, err := os.Pipe()
	if err != nil {
		return err
	}
	defer outR.Close()
	defer outW.Close()
	cmd := exec.Command("codex", args...)
	cmd.Dir = c.WorkDir
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = stderr
	// A dedicated group lets normal worker shutdown terminate Codex descendants.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = cmd.Start(); err != nil {
		return err
	}
	inR.Close()
	outW.Close()
	childDone := make(chan error, 1)
	go func() { childDone <- cmd.Wait() }()
	childReaped := false
	var stopOnce sync.Once
	stopProcess := func() {
		stopOnce.Do(func() {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			if !childReaped {
				<-childDone
			}
		})
	}
	defer stopProcess()
	s, err := NewSession(&pipeTransport{inW, outR}, dir, handle, c, operationTimeout)
	if err != nil {
		return err
	}
	defer s.Close()
	startCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	err = s.Initialize(startCtx)
	cancel()
	if err != nil {
		return err
	}
	var handlers sync.WaitGroup
	acceptDone := make(chan struct{})
	slots := make(chan struct{}, 16)
	stopped := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, e := l.Accept()
			if e != nil {
				return
			}
			select {
			case slots <- struct{}{}:
			default:
				conn.Close()
				continue
			}
			handlers.Add(1)
			go func() {
				defer handlers.Done()
				defer func() { <-slots }()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * operationTimeout))
				var request Command
				if e := json.NewDecoder(io.LimitReader(conn, maxMessage)).Decode(&request); e != nil {
					return
				}
				callCtx, cancel := context.WithTimeout(ctx, operationTimeout)
				defer cancel()
				var e error
				switch request.Op {
				case "status", "questions":
				case "steer":
					e = s.Steer(callCtx, request.Text)
				case "answer":
					e = s.Answer(callCtx, request.RequestID, request.Answers)
				case "kill":
					s.Kill()
					<-stopped
				default:
					e = fmt.Errorf("unknown worker operation %q", request.Op)
				}
				response := reply{Status: s.Status()}
				if e != nil {
					response.Error = e.Error()
				}
				_ = json.NewEncoder(conn).Encode(response)
			}()
		}
	}()
	defer func() {
		l.Close()
		<-acceptDone
		stopProcess()
		os.Remove(socketPath(root, handle))
		close(stopped)
		handlers.Wait()
	}()
	if err = json.NewEncoder(ready).Encode(reply{Status: s.Status()}); err != nil {
		return err
	}
	announced = true
	ready.Close()
	select {
	case <-ctx.Done():
		s.Kill()
	case <-s.Done():
	case exitErr := <-childDone:
		childReaped = true
		// Drain final stdout before marking an exited child disconnected. Descendants
		// retaining the pipe cannot keep a dead session apparently alive indefinitely.
		select {
		case <-s.Done():
		case <-time.After(100 * time.Millisecond):
			s.fail(fmt.Errorf("app-server process exited: %v", exitErr), "disconnected")
		}
	}
	return nil
}

func readStatus(root, handle string) (Status, error) {
	var st Status
	dir, err := sessionPath(root, handle)
	if err != nil {
		return st, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(b, &st)
	return st, err
}

func Call(ctx context.Context, root, handle string, command Command) (Status, error) {
	var st Status
	if _, err := sessionPath(root, handle); err != nil {
		return st, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*operationTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath(root, handle))
	if err != nil {
		return st, err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	if err = json.NewEncoder(conn).Encode(command); err != nil {
		return st, err
	}
	var out reply
	if err = json.NewDecoder(io.LimitReader(conn, maxMessage)).Decode(&out); err != nil {
		return st, err
	}
	if out.Error != "" {
		return out.Status, errors.New(out.Error)
	}
	return out.Status, nil
}

func Inspect(ctx context.Context, root, handle string) (Status, error) {
	st, err := Call(ctx, root, handle, Command{Op: "status"})
	if err == nil {
		return st, nil
	}
	disk, readErr := readStatus(root, handle)
	if readErr != nil {
		return disk, readErr
	}
	if disk.Phase != "killed" && disk.Phase != "disconnected" && disk.Phase != "error" {
		disk.Phase = "stale"
		disk.Error = fmt.Sprintf("worker unavailable: %v", err)
	}
	return disk, nil
}

// Kill waits for normal cleanup before acknowledging. For a dead worker, only
// its stale socket/state are cleaned; disk PIDs are never trusted for signaling.
func Kill(ctx context.Context, root, handle string) error {
	_, err := Call(ctx, root, handle, Command{Op: "kill"})
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.ECONNREFUSED) && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	st, readErr := readStatus(root, handle)
	if readErr != nil {
		return readErr
	}
	if err := os.Remove(socketPath(root, handle)); err != nil && !os.IsNotExist(err) {
		return err
	}
	st.Phase = "killed"
	st.Error = "worker unavailable; stale endpoint cleaned without signaling disk PID"
	st.UpdatedAt = time.Now().UTC()
	return writeJSON(filepath.Join(root, handle, "state.json"), st)
}
