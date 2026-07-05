package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const DefaultTestTimeout = 10 * time.Second

type TestResult struct {
	Server     string     `json:"server"`
	Transport  Transport  `json:"transport"`
	OK         bool       `json:"ok"`
	DurationMS int64      `json:"duration_ms"`
	Steps      []TestStep `json:"steps"`
	Logs       []string   `json:"logs"`
	Error      string     `json:"error,omitempty"`
	ToolCount  int        `json:"tool_count"`
	StderrTail string     `json:"stderr_tail,omitempty"`
}

type TestStep struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Detail     string `json:"detail,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

type testRun struct {
	result TestResult
	start  time.Time
}

func TestServer(ctx context.Context, server Server) TestResult {
	return TestServerWithTimeout(ctx, server, DefaultTestTimeout)
}

func TestServerWithTimeout(ctx context.Context, server Server, timeout time.Duration) TestResult {
	if timeout <= 0 {
		timeout = DefaultTestTimeout
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	run := &testRun{
		start: started,
		result: TestResult{
			Server:    server.Name,
			Transport: server.Transport,
		},
	}
	if !run.step("validate config", func() (string, error) {
		if err := ValidateServer(server); err != nil {
			return "", err
		}
		switch server.Transport {
		case TransportStdio:
			return fmt.Sprintf("command=%s args=%d", RedactSensitive(server.Command), len(server.Args)), nil
		case TransportHTTP:
			return "url=" + RedactSensitive(server.URL), nil
		default:
			return "", nil
		}
	}) {
		return run.finish()
	}
	switch server.Transport {
	case TransportStdio:
		run.testStdio(ctx, server)
	case TransportHTTP:
		run.testHTTP(ctx, server)
	}
	return run.finish()
}

func (r *testRun) step(name string, fn func() (string, error)) bool {
	started := time.Now()
	detail, err := fn()
	step := TestStep{Name: name, Status: "ok", Detail: RedactSensitive(detail), DurationMS: time.Since(started).Milliseconds()}
	if err != nil {
		step.Status = "error"
		step.Detail = RedactSensitive(err.Error())
		r.result.Error = step.Detail
		r.result.Steps = append(r.result.Steps, step)
		r.log("%s: error: %s", name, step.Detail)
		return false
	}
	r.result.Steps = append(r.result.Steps, step)
	if detail == "" {
		r.log("%s: ok", name)
	} else {
		r.log("%s: ok (%s)", name, step.Detail)
	}
	return true
}

func (r *testRun) log(format string, args ...any) {
	r.result.Logs = append(r.result.Logs, RedactSensitive(fmt.Sprintf(format, args...)))
}

func (r *testRun) cleanupStep(detail string) {
	r.result.Steps = append(r.result.Steps, TestStep{Name: "cleanup", Status: "ok", Detail: RedactSensitive(detail)})
	r.log("cleanup: ok (%s)", detail)
}

func (r *testRun) finish() TestResult {
	r.result.DurationMS = time.Since(r.start).Milliseconds()
	r.result.OK = r.result.Error == ""
	return r.result
}

func (r *testRun) fail(format string, args ...any) {
	msg := RedactSensitive(fmt.Sprintf(format, args...))
	r.result.Error = msg
	r.log("error: %s", msg)
}

func (r *testRun) testStdio(ctx context.Context, server Server) {
	cmd := exec.CommandContext(ctx, server.Command, server.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		r.fail("open stdin: %v", err)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.fail("open stdout: %v", err)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		r.fail("open stderr: %v", err)
		return
	}
	lines := make(chan []byte, 32)
	var stderrTail tailBuffer
	var wg sync.WaitGroup
	if !r.step("start/connect", func() (string, error) {
		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("start %s: %w", server.Command, err)
		}
		wg.Add(2)
		go scanLines(&wg, stdout, lines)
		go copyTail(&wg, stderr, &stderrTail)
		return fmt.Sprintf("pid=%d", cmd.Process.Pid), nil
	}) {
		_ = stdin.Close()
		return
	}
	waitDone := make(chan error, 1)
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		cleanupDetail := "process stopped"
		select {
		case err := <-waitDone:
			if err == nil {
				cleanupDetail = "process exited"
			}
		case <-time.After(2 * time.Second):
			cleanupDetail = "process did not exit within 2s"
		}
		wg.Wait()
		if tail := stderrTail.String(); tail != "" {
			r.result.StderrTail = RedactSensitive(tail)
		}
		r.cleanupStep(cleanupDetail)
	}()
	go func() { waitDone <- cmd.Wait() }()

	if !r.step("initialize", func() (string, error) {
		resp, err := stdioRequest(ctx, stdin, lines, 1, "initialize", initializeParams())
		if err != nil {
			return "", err
		}
		if resp.Error != nil {
			return "", fmt.Errorf("initialize rpc error: %s", string(resp.Error))
		}
		return protocolVersion(resp.Result), nil
	}) {
		return
	}
	if !r.step("initialized", func() (string, error) {
		return "", stdioNotification(ctx, stdin, "initialized", map[string]any{})
	}) {
		return
	}
	r.step("tools/list", func() (string, error) {
		resp, err := stdioRequest(ctx, stdin, lines, 2, "tools/list", map[string]any{})
		if err != nil {
			return "", err
		}
		if resp.Error != nil {
			return "", fmt.Errorf("tools/list rpc error: %s", string(resp.Error))
		}
		n := toolCount(resp.Result)
		r.result.ToolCount = n
		return fmt.Sprintf("%d tools", n), nil
	})
}

func scanLines(wg *sync.WaitGroup, r io.Reader, out chan<- []byte) {
	defer wg.Done()
	defer close(out)
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		copied := append([]byte(nil), line...)
		out <- copied
	}
}

func copyTail(wg *sync.WaitGroup, r io.Reader, tail *tailBuffer) {
	defer wg.Done()
	_, _ = io.Copy(tail, r)
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

func stdioRequest(ctx context.Context, w io.Writer, lines <-chan []byte, id int, method string, params any) (rpcMessage, error) {
	req := rpcMessage{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	if err := writeRPC(w, req); err != nil {
		return rpcMessage{}, err
	}
	return readRPC(ctx, lines, id)
}

func stdioNotification(ctx context.Context, w io.Writer, method string, params any) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return writeRPC(w, rpcMessage{JSONRPC: "2.0", Method: method, Params: params})
}

func writeRPC(w io.Writer, msg rpcMessage) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	_, err = w.Write(raw)
	return err
}

func readRPC(ctx context.Context, lines <-chan []byte, id int) (rpcMessage, error) {
	for {
		select {
		case <-ctx.Done():
			return rpcMessage{}, ctx.Err()
		case line, ok := <-lines:
			if !ok {
				return rpcMessage{}, errors.New("server closed stdout before response")
			}
			var msg rpcMessage
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			if rpcID(msg.ID) != id {
				continue
			}
			return msg, nil
		}
	}
}

func (r *testRun) testHTTP(ctx context.Context, server Server) {
	defer r.cleanupStep("http request complete")
	client := &http.Client{Timeout: DefaultTestTimeout}
	sessionID := ""
	if !r.step("start/connect", func() (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
		if err != nil {
			return "", err
		}
		setMCPHTTPHeaders(req)
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		sessionID = resp.Header.Get("Mcp-Session-Id")
		msg, err := readHTTPRPC(resp)
		if err != nil {
			return "", err
		}
		if msg.Error != nil {
			return "", fmt.Errorf("initialize rpc error: %s", string(msg.Error))
		}
		return "http " + resp.Status + " " + protocolVersion(msg.Result), nil
	}) {
		return
	}
	if !r.step("initialize", func() (string, error) {
		return "already completed during connect", nil
	}) {
		return
	}
	_ = r.step("initialized", func() (string, error) {
		return httpNotify(ctx, client, server.URL, sessionID, "initialized")
	})
	if r.result.Error != "" {
		return
	}
	r.step("tools/list", func() (string, error) {
		msg, err := httpRequest(ctx, client, server.URL, sessionID, 2, "tools/list", map[string]any{})
		if err != nil {
			return "", err
		}
		if msg.Error != nil {
			return "", fmt.Errorf("tools/list rpc error: %s", string(msg.Error))
		}
		n := toolCount(msg.Result)
		r.result.ToolCount = n
		return fmt.Sprintf("%d tools", n), nil
	})
}

func initializeParams() map[string]any {
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "podiom-mcp-test", "version": "0"},
	}
}

func httpRequest(ctx context.Context, client *http.Client, endpoint, sessionID string, id int, method string, params any) (rpcMessage, error) {
	payload, _ := json.Marshal(rpcMessage{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return rpcMessage{}, err
	}
	setMCPHTTPHeaders(req)
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := client.Do(req)
	if err != nil {
		return rpcMessage{}, err
	}
	defer resp.Body.Close()
	return readHTTPRPC(resp)
}

func httpNotify(ctx context.Context, client *http.Client, endpoint, sessionID, method string) (string, error) {
	payload, _ := json.Marshal(rpcMessage{JSONRPC: "2.0", Method: method, Params: map[string]any{}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	setMCPHTTPHeaders(req)
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return resp.Status, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("http %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return resp.Status, nil
}

func setMCPHTTPHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
}

func readHTTPRPC(resp *http.Response) (rpcMessage, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return rpcMessage{}, fmt.Errorf("http %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/event-stream") {
		return readSSE(resp.Body)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return rpcMessage{}, err
	}
	return decodeRPCPayload(raw)
}

func readSSE(r io.Reader) (rpcMessage, error) {
	scanner := bufio.NewScanner(r)
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		if strings.TrimSpace(line) == "" && data.Len() > 0 {
			return decodeRPCPayload([]byte(data.String()))
		}
	}
	if scanner.Err() != nil {
		return rpcMessage{}, scanner.Err()
	}
	if data.Len() > 0 {
		return decodeRPCPayload([]byte(data.String()))
	}
	return rpcMessage{}, errors.New("empty SSE response")
}

func decodeRPCPayload(raw []byte) (rpcMessage, error) {
	raw = bytes.TrimSpace(raw)
	var msg rpcMessage
	if err := json.Unmarshal(raw, &msg); err == nil && (msg.JSONRPC != "" || msg.ID != nil || msg.Result != nil || msg.Error != nil) {
		return msg, nil
	}
	var batch []rpcMessage
	if err := json.Unmarshal(raw, &batch); err != nil {
		return rpcMessage{}, err
	}
	for _, msg := range batch {
		if msg.ID != nil || msg.Result != nil || msg.Error != nil {
			return msg, nil
		}
	}
	return rpcMessage{}, errors.New("no JSON-RPC response in payload")
}

func rpcID(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case json.Number:
		n, _ := x.Int64()
		return int(n)
	default:
		return -1
	}
}

func protocolVersion(raw json.RawMessage) string {
	var body struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.ProtocolVersion == "" {
		return ""
	}
	return "protocol=" + body.ProtocolVersion
}

func toolCount(raw json.RawMessage) int {
	var body struct {
		Tools []any `json:"tools"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return 0
	}
	return len(body.Tools)
}

type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	const max = 16 * 1024
	if len(t.buf) > max {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

func RedactSensitive(value string) string {
	if value == "" {
		return ""
	}
	value = redactURLs(value)
	value = tokenValueRE.ReplaceAllString(value, "$1=<redacted>")
	value = bearerRE.ReplaceAllString(value, "$1 <redacted>")
	value = privateSegmentRE.ReplaceAllString(value, "/<redacted>")
	return value
}

var (
	tokenValueRE     = regexp.MustCompile(`(?i)\b(token|secret|key|password|authorization|auth|api[_-]?key)=([^\s&]+)`)
	bearerRE         = regexp.MustCompile(`(?i)\b(Bearer)\s+[-._~+/A-Za-z0-9]+=*`)
	privateSegmentRE = regexp.MustCompile(`/(private|token|secret|key|password|auth)[^/\s]*`)
)

func redactURLs(value string) string {
	fields := strings.Fields(value)
	for _, field := range fields {
		trimmed := strings.Trim(field, `"'(),`)
		if !strings.Contains(trimmed, "://") {
			continue
		}
		u, err := url.Parse(trimmed)
		if err != nil || u.Scheme == "" || u.Host == "" {
			continue
		}
		if u.User != nil {
			u.User = url.User("<redacted>")
		}
		if u.RawQuery != "" {
			u.RawQuery = "redacted=1"
		}
		redacted := u.String()
		value = strings.ReplaceAll(value, trimmed, redacted)
	}
	return value
}
