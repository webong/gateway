package runtime

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/webong/gateway/arranger/automation"
	"golang.org/x/crypto/ssh"
)

const sshHelperCommand = "gateway-shell-helper"

var sshReferencePattern = regexp.MustCompile(`^ssh:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var sshHookPattern = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*(/[A-Za-z0-9_-][A-Za-z0-9._-]*)*$`)

// SSHExecution is resolved privately just before a run; it never enters a route plan.
type SSHExecution struct {
	Hook          string `json:"hook"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	User          string `json:"user"`
	HostPublicKey string `json:"host_public_key"`
	PrivateKey    string `json:"private_key"`
}

type SSHResolver interface {
	Resolve(context.Context, string) (SSHExecution, error)
}

type sshResolverContextKey struct{}

// SetSSHResolver installs the private Planner credential authority. Nil or no
// deployed outbound CIDR ceiling leaves remote execution unavailable.
func (e *Engine) SetSSHResolver(resolver SSHResolver) {
	if e == nil {
		return
	}
	e.sshMu.Lock()
	defer e.sshMu.Unlock()
	e.sshResolver = resolver
}

func splitCIDRs(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var result []string
	for _, part := range strings.Split(value, ",") {
		result = append(result, strings.TrimSpace(part))
	}
	return result
}

func (e *Engine) executeSSH(ctx context.Context, source string, input automation.Input) (outcome automation.Outcome, execErr error) {
	if !sshReferencePattern.MatchString(source) {
		return automation.Outcome{}, errors.New("SSH Shell execution reference is invalid")
	}
	e.sshMu.RLock()
	resolver := e.sshResolver
	e.sshMu.RUnlock()
	if scoped, ok := ctx.Value(sshResolverContextKey{}).(SSHResolver); ok {
		resolver = scoped
	}
	if resolver == nil || len(e.sshAllowed) == 0 {
		return automation.Outcome{}, errors.New("SSH Shell execution is not configured")
	}
	release, err := e.admit(ctx)
	if err != nil {
		return automation.Outcome{}, err
	}
	defer release()
	started := time.Now().UTC()
	runID, err := newRunID()
	if err != nil {
		return automation.Outcome{}, err
	}
	record := runRecord{ID: runID, Hook: source, StartedAt: started, DeliveryID: eventString(input.Event, "delivery_id"), ExitCode: -1}
	defer func() {
		record.DurationMS = time.Since(started).Milliseconds()
		if execErr != nil {
			record.Error = execErr.Error()
		}
		if err := e.writeRecord(record); err != nil {
			outcome = automation.Outcome{}
			execErr = fmt.Errorf("record SSH Shell run: %w", err)
		}
		if execErr != nil {
			execErr = &RunFailure{ID: runID, Cause: execErr}
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, e.config.Timeout)
	defer cancel()
	resolved, err := resolver.Resolve(ctx, strings.TrimPrefix(source, "ssh:"))
	if err != nil {
		return automation.Outcome{}, errors.New("SSH Shell credential authority is unavailable")
	}
	if err := validateSSHExecution(resolved); err != nil {
		return automation.Outcome{}, err
	}
	record.Hook = resolved.Hook
	request, err := json.Marshal(map[string]any{"hook": resolved.Hook, "run_id": runID,
		"event": input.Event, "variables": input.Variables})
	if err != nil {
		return automation.Outcome{}, errors.New("encode SSH Shell input failed")
	}
	if len(request) > 1<<20 {
		return automation.Outcome{}, errors.New("SSH Shell input exceeds limit")
	}
	conn, err := e.dialSSH(ctx, resolved)
	if err != nil {
		return automation.Outcome{}, err
	}
	defer conn.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	client, err := e.sshClient(conn, resolved)
	if err != nil {
		return automation.Outcome{}, err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return automation.Outcome{}, errors.New("SSH Shell session unavailable")
	}
	defer session.Close()
	stdout := &limitedBuffer{limit: e.config.MaxOutputBytes}
	stderr := &limitedBuffer{limit: e.config.MaxOutputBytes}
	session.Stdin = bytes.NewReader(request)
	session.Stdout = stdout
	session.Stderr = stderr
	err = session.Run(sshHelperCommand)
	record.Stdout, record.Stderr = stdout.String(), stderr.String()
	if stdout.exceeded || stderr.exceeded {
		return automation.Outcome{}, errors.New("SSH Shell output exceeded limit")
	}
	if ctx.Err() != nil {
		return automation.Outcome{}, fmt.Errorf("SSH Shell deadline exceeded: %w", ctx.Err())
	}
	if err != nil {
		return automation.Outcome{}, errors.New("SSH Shell remote helper failed; outcome may be unknown")
	}
	if err := json.Unmarshal(stdout.Bytes(), &outcome); err != nil {
		return automation.Outcome{}, errors.New("SSH Shell remote outcome is invalid")
	}
	if len(outcome.Schedules) > 0 {
		return automation.Outcome{}, errors.New("SSH Shell cannot declare schedules")
	}
	if outcome.Response != nil {
		if outcome.Response.Headers == nil {
			outcome.Response.Headers = make(map[string][]string)
		}
		outcome.Response.Headers["X-Gateway-Shell-Run-Id"] = []string{runID}
	}
	outcome.RunID = runID
	record.ExitCode = 0
	return outcome, nil
}

func validateSSHExecution(value SSHExecution) error {
	if value.Hook == "" || len(value.Hook) > 1024 || !sshHookPattern.MatchString(value.Hook) ||
		value.Host == "" || strings.ContainsAny(value.Host, "/:@ ") ||
		value.Port < 1 || value.Port > 65535 || value.User == "" || strings.ContainsAny(value.User, "/:@ ") ||
		value.HostPublicKey == "" || value.PrivateKey == "" {
		return errors.New("SSH Shell execution is invalid")
	}
	return nil
}

func (e *Engine) dialSSH(ctx context.Context, value SSHExecution) (net.Conn, error) {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, value.Host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("SSH Shell host resolution failed")
	}
	for _, address := range addresses {
		allowed := false
		for _, ceiling := range e.sshAllowed {
			if ceiling.Contains(address.IP) {
				allowed = true
				break
			}
		}
		if !allowed || address.Zone != "" {
			return nil, errors.New("SSH Shell destination is outside the deployed network ceiling")
		}
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(addresses[0].IP.String(), strconv.Itoa(value.Port)))
	if err != nil {
		return nil, errors.New("SSH Shell destination unavailable")
	}
	return conn, nil
}

func (e *Engine) sshClient(conn net.Conn, value SSHExecution) (*ssh.Client, error) {
	pinned, rest, _, _, err := ssh.ParseAuthorizedKey([]byte(value.HostPublicKey))
	if err != nil || strings.TrimSpace(rest) != "" {
		return nil, errors.New("SSH Shell host identity is invalid")
	}
	signer, err := ssh.ParsePrivateKey([]byte(value.PrivateKey))
	if err != nil {
		return nil, errors.New("SSH Shell client identity is invalid")
	}
	config := &ssh.ClientConfig{User: value.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: func(_ string, _ net.Addr, remote ssh.PublicKey) error {
			if subtle.ConstantTimeCompare(pinned.Marshal(), remote.Marshal()) != 1 {
				return errors.New("SSH Shell host identity mismatch")
			}
			return nil
		}, Timeout: 5 * time.Second}
	connection, channels, requests, err := ssh.NewClientConn(conn, net.JoinHostPort(value.Host, strconv.Itoa(value.Port)), config)
	if err != nil {
		return nil, errors.New("SSH Shell handshake failed")
	}
	return ssh.NewClient(connection, channels, requests), nil
}
