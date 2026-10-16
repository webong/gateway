// Package imap provides TLS-only IMAP access to an external mailbox server.
// Planner authenticates clients; the upstream owns all mailbox state.
package imap

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

var ErrServerClosed = errors.New("IMAP server closed")
var validTag = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var literalMarker = regexp.MustCompile(`^\{([0-9]+)(\+?)\}$`)

const maxAuthLiteral = 2048

type Upstream struct {
	Address    string `json:"address"`
	ServerName string `json:"server_name"`
}

type Config struct {
	Address              string
	Hostname             string
	TLSConfig            *tls.Config
	Upstreams            map[string]Upstream
	AllowedUpstreamHosts []string
	UpstreamRootCAs      *x509.CertPool
	MaxConnections       int
	AuthPerMinute        int
	AuthBurst            int
	IdleTimeout          time.Duration
	PlannerTimeout       time.Duration
	UpstreamTimeout      time.Duration
}

type Server struct {
	config    Config
	planner   bridge.ProtocolPlanner
	slots     chan struct{}
	limiter   *authLimiter
	listener  net.Listener
	serveDone chan struct{}
	started   bool
	stop      chan struct{}
	once      sync.Once
	mu        sync.Mutex
	clients   map[net.Conn]struct{}
	wait      sync.WaitGroup
}

func LoadTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	if certFile == "" || keyFile == "" {
		return nil, errors.New("IMAP requires a TLS certificate and private key")
	}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load IMAP TLS certificate: %w", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}, nil
}

func NewServer(config Config, planner bridge.ProtocolPlanner) (*Server, error) {
	if config.Address == "" || config.TLSConfig == nil ||
		(len(config.TLSConfig.Certificates) == 0 && config.TLSConfig.GetCertificate == nil) || planner == nil {
		return nil, errors.New("IMAP listener requires an address, TLS certificate, and Planner")
	}
	if config.MaxConnections == 0 {
		config.MaxConnections = 1000
	}
	if config.AuthPerMinute == 0 {
		config.AuthPerMinute = 30
	}
	if config.AuthBurst == 0 {
		config.AuthBurst = 5
	}
	if config.IdleTimeout == 0 {
		config.IdleTimeout = 30 * time.Minute
	}
	if config.PlannerTimeout == 0 {
		config.PlannerTimeout = 10 * time.Second
	}
	if config.UpstreamTimeout == 0 {
		config.UpstreamTimeout = 10 * time.Second
	}
	if config.MaxConnections < 1 || config.AuthPerMinute < 1 || config.AuthBurst < 1 || config.IdleTimeout < 10*time.Minute || config.PlannerTimeout < 1 || config.UpstreamTimeout < 1 {
		return nil, errors.New("IMAP connection and timeout limits are invalid")
	}
	if len(config.Upstreams) == 0 && len(config.AllowedUpstreamHosts) == 0 {
		return nil, errors.New("IMAP requires a registered mailbox upstream")
	}
	for id, upstream := range config.Upstreams {
		if !validTag.MatchString(id) || upstream.Address == "" || upstream.ServerName == "" {
			return nil, fmt.Errorf("invalid IMAP upstream %q", id)
		}
		if _, _, err := net.SplitHostPort(upstream.Address); err != nil {
			return nil, fmt.Errorf("invalid IMAP upstream %q address: %w", id, err)
		}
	}
	config.TLSConfig = config.TLSConfig.Clone()
	config.TLSConfig.MinVersion = tls.VersionTLS12
	return &Server{config: config, planner: planner, slots: make(chan struct{}, config.MaxConnections),
		limiter: newAuthLimiter(config.AuthPerMinute, config.AuthBurst),
		stop:    make(chan struct{}), serveDone: make(chan struct{}), clients: make(map[net.Conn]struct{})}, nil
}

func (s *Server) ListenAndServe() error {
	listener, err := net.Listen("tcp", s.config.Address)
	if err != nil {
		return err
	}
	return s.Serve(listener)
}

func (s *Server) Serve(listener net.Listener) error {
	if listener == nil {
		return errors.New("IMAP listener is required")
	}
	secure := tls.NewListener(listener, s.config.TLSConfig)
	s.mu.Lock()
	select {
	case <-s.stop:
		s.mu.Unlock()
		_ = secure.Close()
		return ErrServerClosed
	default:
	}
	if s.started {
		s.mu.Unlock()
		_ = secure.Close()
		return errors.New("IMAP listener is already running")
	}
	s.started = true
	s.listener = secure
	s.mu.Unlock()
	defer close(s.serveDone)
	for {
		connection, err := secure.Accept()
		if err != nil {
			select {
			case <-s.stop:
				return ErrServerClosed
			default:
				return err
			}
		}
		select {
		case s.slots <- struct{}{}:
		default:
			_ = connection.Close()
			continue
		}
		s.mu.Lock()
		s.clients[connection] = struct{}{}
		s.wait.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wait.Done()
			defer func() {
				_ = connection.Close()
				s.mu.Lock()
				delete(s.clients, connection)
				s.mu.Unlock()
				<-s.slots
			}()
			s.handle(connection)
		}()
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.once.Do(func() { close(s.stop) })
	s.mu.Lock()
	started := s.started
	if s.listener != nil {
		_ = s.listener.Close()
	}
	for connection := range s.clients {
		_ = connection.Close()
	}
	s.mu.Unlock()
	if started {
		select {
		case <-s.serveDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan struct{})
	go func() { s.wait.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) handle(connection net.Conn) {
	if err := connection.SetDeadline(time.Now().Add(s.config.PlannerTimeout)); err != nil {
		return
	}
	if err := connection.(*tls.Conn).Handshake(); err != nil {
		return
	}
	_ = connection.SetDeadline(time.Now().Add(s.config.IdleTimeout))
	reader := bufio.NewReaderSize(connection, 4096)
	if _, err := io.WriteString(connection, "* OK [CAPABILITY IMAP4rev1 AUTH=PLAIN SASL-IR] Gateway ready\r\n"); err != nil {
		return
	}
	sessionID := randomID()
	attempts := 0
	for {
		_ = connection.SetReadDeadline(time.Now().Add(s.config.IdleTimeout))
		line, err := readLine(reader, 4096)
		if err != nil {
			return
		}
		tag, command, args, ok := parseCommand(line)
		if !ok {
			_, _ = io.WriteString(connection, "* BAD invalid command\r\n")
			continue
		}
		switch command {
		case "CAPABILITY":
			_, err = io.WriteString(connection, "* CAPABILITY IMAP4rev1 AUTH=PLAIN SASL-IR\r\n"+tag+" OK CAPABILITY completed\r\n")
			if err != nil {
				return
			}
		case "NOOP":
			if _, err = io.WriteString(connection, tag+" OK NOOP completed\r\n"); err != nil {
				return
			}
		case "LOGOUT":
			_, _ = io.WriteString(connection, "* BYE Gateway closing\r\n"+tag+" OK LOGOUT completed\r\n")
			return
		case "LOGIN", "AUTHENTICATE":
			var username, password string
			if command == "LOGIN" {
				var fatal bool
				username, password, ok, fatal = s.readLogin(connection, reader, args)
				if fatal {
					_, _ = io.WriteString(connection, tag+" BAD invalid or oversized login literal\r\n")
					return
				}
			} else {
				username, password, ok = s.readPlainAuth(connection, reader, args)
			}
			if !ok {
				_, _ = io.WriteString(connection, tag+" BAD unsupported authentication syntax\r\n")
				continue
			}
			if !s.limiter.allow(connection.RemoteAddr().String()) {
				_, _ = io.WriteString(connection, tag+" NO authentication rate limit exceeded\r\n")
				return
			}
			attempts++
			grant, planErr := s.authenticate(sessionID, connection.RemoteAddr().String(), username, password)
			if planErr != nil {
				_, _ = io.WriteString(connection, tag+" NO mailbox service unavailable\r\n")
				return
			}
			if grant == nil {
				_, _ = io.WriteString(connection, tag+" NO authentication failed\r\n")
				if attempts >= 3 {
					return
				}
				continue
			}
			upstream, upstreamReader, err := s.connectUpstream(grant)
			if err != nil {
				_, _ = io.WriteString(connection, tag+" NO mailbox service unavailable\r\n")
				return
			}
			defer upstream.Close()
			if err := s.audit(sessionID, username, connection.RemoteAddr().String(), bridge.EventConnect, grant); err != nil {
				_, _ = io.WriteString(connection, tag+" NO mailbox service unavailable\r\n")
				return
			}
			defer func() { _ = s.audit(sessionID, username, connection.RemoteAddr().String(), bridge.EventClose, grant) }()
			if _, err := io.WriteString(connection, tag+" OK "+command+" completed\r\n"); err != nil {
				return
			}
			s.proxy(connection, reader, upstream, upstreamReader)
			return
		default:
			if _, err := io.WriteString(connection, tag+" BAD authenticate first\r\n"); err != nil {
				return
			}
		}
	}
}

type mailboxGrant struct {
	identity  string
	id        string
	upstream  Upstream
	username  string
	password  string
	mechanism string
}

func (s *Server) authenticate(sessionID, remoteAddr, username, password string) (*mailboxGrant, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.config.PlannerTimeout)
	defer cancel()
	decision, err := s.planner.PlanEvent(ctx, bridge.GatewayEvent{
		ID: randomID(), Protocol: bridge.ProtocolIMAP, Kind: bridge.EventAuthenticate,
		SessionID: sessionID, Host: s.config.Hostname, Route: username,
		Attributes: map[string]string{"username": username, "password": password, "remote_addr": remoteAddr},
	})
	if err != nil || decision.Validate() != nil || decision.Protocol != bridge.ProtocolIMAP {
		return nil, errors.New("IMAP authentication planning failed")
	}
	if decision.Action != bridge.GatewayAccept {
		return nil, nil
	}
	id := decision.Metadata["upstream_id"]
	upstream, ok := s.config.Upstreams[id]
	if address := decision.Metadata["upstream_address"]; address != "" {
		name := decision.Metadata["upstream_server_name"]
		if !bridge.ApprovedMailUpstream(address, name, s.config.AllowedUpstreamHosts) {
			return nil, errors.New("IMAP upstream is outside the deployed host ceiling")
		}
		upstream, ok = Upstream{Address: address, ServerName: name}, true
	}
	identity := decision.Metadata["identity"]
	upstreamUsername := decision.Metadata["upstream_username"]
	upstreamPassword := decision.Metadata["upstream_password"]
	mechanism := decision.Metadata["upstream_auth_mechanism"]
	if mechanism == "" {
		mechanism = "LOGIN"
	}
	passwordValid := validMappedCredential(upstreamPassword)
	if mechanism == "XOAUTH2" {
		passwordValid = validMappedToken(upstreamPassword)
	}
	if !ok || identity == "" || !validMappedCredential(upstreamUsername) || !passwordValid || (mechanism != "LOGIN" && mechanism != "XOAUTH2") {
		return nil, errors.New("IMAP authentication decision has invalid upstream credentials")
	}
	return &mailboxGrant{identity: identity, id: id, upstream: upstream, username: upstreamUsername, password: upstreamPassword, mechanism: mechanism}, nil
}

func (s *Server) connectUpstream(grant *mailboxGrant) (net.Conn, *bufio.Reader, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.config.UpstreamTimeout)
	defer cancel()
	connection, err := (&tls.Dialer{NetDialer: &net.Dialer{Timeout: s.config.UpstreamTimeout},
		Config: &tls.Config{ServerName: grant.upstream.ServerName, RootCAs: s.config.UpstreamRootCAs, MinVersion: tls.VersionTLS12},
	}).DialContext(ctx, "tcp", grant.upstream.Address)
	if err != nil {
		return nil, nil, err
	}
	_ = connection.SetDeadline(time.Now().Add(s.config.UpstreamTimeout))
	reader := bufio.NewReaderSize(connection, 4096)
	greeting, err := readLine(reader, 4096)
	if err != nil || !strings.HasPrefix(greeting, "* OK") {
		_ = connection.Close()
		return nil, nil, errors.New("IMAP upstream did not greet successfully")
	}
	command := "GATEWAY1 LOGIN " + quote(grant.username) + " " + quote(grant.password) + "\r\n"
	if grant.mechanism == "XOAUTH2" {
		initial := "user=" + grant.username + "\x01auth=Bearer " + grant.password + "\x01\x01"
		command = "GATEWAY1 AUTHENTICATE XOAUTH2 " + base64.StdEncoding.EncodeToString([]byte(initial)) + "\r\n"
	}
	_, err = io.WriteString(connection, command)
	if err != nil {
		_ = connection.Close()
		return nil, nil, err
	}
	for i := 0; i < 32; i++ {
		line, err := readLine(reader, 4096)
		if err != nil {
			_ = connection.Close()
			return nil, nil, err
		}
		if strings.HasPrefix(line, "GATEWAY1 ") {
			if !strings.HasPrefix(line, "GATEWAY1 OK") {
				_ = connection.Close()
				return nil, nil, errors.New("IMAP upstream rejected mapped credentials")
			}
			_ = connection.SetDeadline(time.Time{})
			return connection, reader, nil
		}
		if !strings.HasPrefix(line, "* ") {
			_ = connection.Close()
			return nil, nil, errors.New("IMAP upstream login response is invalid")
		}
	}
	_ = connection.Close()
	return nil, nil, errors.New("IMAP upstream login response exceeded limit")
}

func (s *Server) audit(sessionID, username, remoteAddr string, kind bridge.EventKind, grant *mailboxGrant) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.config.PlannerTimeout)
	defer cancel()
	decision, err := s.planner.PlanEvent(ctx, bridge.GatewayEvent{
		ID: randomID(), Protocol: bridge.ProtocolIMAP, Kind: kind,
		SessionID: sessionID, Host: s.config.Hostname, Route: username,
		Attributes: map[string]string{"identity": grant.identity, "upstream_id": grant.id, "remote_addr": remoteAddr},
	})
	if err != nil || decision.Validate() != nil || decision.Protocol != bridge.ProtocolIMAP || decision.Action != bridge.GatewayAccept {
		return errors.New("IMAP session audit planning failed")
	}
	return nil
}

func (s *Server) proxy(client net.Conn, clientReader *bufio.Reader, upstream net.Conn, upstreamReader *bufio.Reader) {
	clientRead := &idleReader{Reader: clientReader, Conn: client, timeout: s.config.IdleTimeout}
	upstreamRead := &idleReader{Reader: upstreamReader, Conn: upstream, timeout: s.config.IdleTimeout}
	clientWrite := &idleWriter{Conn: client, timeout: s.config.IdleTimeout}
	upstreamWrite := &idleWriter{Conn: upstream, timeout: s.config.IdleTimeout}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstreamWrite, clientRead); done <- struct{}{} }()
	go func() { _, _ = io.Copy(clientWrite, upstreamRead); done <- struct{}{} }()
	<-done
	_ = client.Close()
	_ = upstream.Close()
	<-done
}

type idleReader struct {
	io.Reader
	net.Conn
	timeout time.Duration
}

func (r *idleReader) Read(data []byte) (int, error) {
	_ = r.Conn.SetReadDeadline(time.Now().Add(r.timeout))
	return r.Reader.Read(data)
}

type idleWriter struct {
	net.Conn
	timeout time.Duration
}

func (w *idleWriter) Write(data []byte) (int, error) {
	_ = w.Conn.SetWriteDeadline(time.Now().Add(w.timeout))
	return w.Conn.Write(data)
}

func readLine(reader *bufio.Reader, max int) (string, error) {
	line, err := reader.ReadSlice('\n')
	if err != nil || len(line) > max || !bytes.HasSuffix(line, []byte("\r\n")) {
		return "", errors.New("IMAP line is invalid or too long")
	}
	return string(line[:len(line)-2]), nil
}

func randomID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic("IMAP session entropy is unavailable")
	}
	return hex.EncodeToString(data[:])
}

func quote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

func validMappedCredential(value string) bool {
	if value == "" || len(value) > 248 {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char > 0x7e {
			return false
		}
	}
	return true
}

func validMappedToken(value string) bool {
	if value == "" || len(value) > 4096 {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char > 0x7e {
			return false
		}
	}
	return true
}

func parseCommand(line string) (tag, command, args string, ok bool) {
	first := strings.IndexByte(line, ' ')
	if first < 1 || !validTag.MatchString(line[:first]) {
		return "", "", "", false
	}
	tag = line[:first]
	rest := strings.TrimLeft(line[first+1:], " ")
	second := strings.IndexByte(rest, ' ')
	if second < 0 {
		return tag, strings.ToUpper(rest), "", rest != ""
	}
	return tag, strings.ToUpper(rest[:second]), strings.TrimLeft(rest[second+1:], " "), true
}

func parseLogin(args string) (username, password string, ok bool) {
	username, rest, ok := parseAstring(args)
	if !ok {
		return "", "", false
	}
	password, rest, ok = parseAstring(strings.TrimLeft(rest, " "))
	if !ok || strings.TrimSpace(rest) != "" || username == "" || password == "" {
		return "", "", false
	}
	return username, password, true
}

// readLogin consumes at most two bounded synchronizing IMAP literals.
func (s *Server) readLogin(connection net.Conn, reader *bufio.Reader, args string) (username, password string, ok, fatal bool) {
	username, rest, valid, stop := s.readLoginArg(connection, reader, args)
	if !valid || stop {
		return "", "", false, stop
	}
	password, rest, valid, stop = s.readLoginArg(connection, reader, strings.TrimLeft(rest, " "))
	if !valid || stop {
		return "", "", false, stop
	}
	if strings.TrimSpace(rest) != "" || !validClientCredential(username) || !validClientCredential(password) {
		return "", "", false, false
	}
	return username, password, true, false
}

func (s *Server) readLoginArg(connection net.Conn, reader *bufio.Reader, args string) (value, rest string, ok, fatal bool) {
	if !strings.HasPrefix(args, "{") {
		value, rest, ok = parseAstring(args)
		return value, rest, ok, false
	}
	marker := literalMarker.FindStringSubmatch(args)
	if marker == nil {
		return "", "", false, false
	}
	size, err := strconv.ParseUint(marker[1], 10, 32)
	if err != nil || size > maxAuthLiteral || marker[2] != "" {
		return "", "", false, true
	}
	if _, err := io.WriteString(connection, "+ Ready for login literal\r\n"); err != nil {
		return "", "", false, true
	}
	_ = connection.SetReadDeadline(time.Now().Add(s.config.PlannerTimeout))
	literal := make([]byte, size)
	if _, err := io.ReadFull(reader, literal); err != nil {
		return "", "", false, true
	}
	rest, err = readLine(reader, 4096)
	if err != nil {
		return "", "", false, true
	}
	return string(literal), rest, true, false
}

func validClientCredential(value string) bool {
	if value == "" || len(value) > maxAuthLiteral || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if char == 0 || char == '\r' || char == '\n' {
			return false
		}
	}
	return true
}

func parseAstring(value string) (string, string, bool) {
	if value == "" {
		return "", "", false
	}
	if value[0] == '"' {
		var result strings.Builder
		for i := 1; i < len(value); i++ {
			switch value[i] {
			case '"':
				return result.String(), value[i+1:], true
			case '\\':
				i++
				if i >= len(value) || (value[i] != '\\' && value[i] != '"') {
					return "", "", false
				}
				result.WriteByte(value[i])
			default:
				if value[i] < 0x20 || value[i] > 0x7e {
					return "", "", false
				}
				result.WriteByte(value[i])
			}
		}
		return "", "", false
	}
	end := strings.IndexByte(value, ' ')
	if end < 0 {
		end = len(value)
	}
	for i := 0; i < end; i++ {
		if value[i] <= 0x20 || value[i] > 0x7e || strings.ContainsRune("(){%*\\\"]", rune(value[i])) {
			return "", "", false
		}
	}
	return value[:end], value[end:], end > 0
}

func (s *Server) readPlainAuth(connection net.Conn, reader *bufio.Reader, args string) (string, string, bool) {
	mechanism, response, hasResponse := strings.Cut(args, " ")
	if !strings.EqualFold(mechanism, "PLAIN") {
		return "", "", false
	}
	if !hasResponse {
		if _, err := io.WriteString(connection, "+ \r\n"); err != nil {
			return "", "", false
		}
		_ = connection.SetReadDeadline(time.Now().Add(s.config.IdleTimeout))
		line, err := readLine(reader, 4096)
		if err != nil || line == "*" {
			return "", "", false
		}
		response = line
	}
	if response == "=" {
		response = ""
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(response)
	if err != nil || len(decoded) > 2048 {
		return "", "", false
	}
	parts := bytes.Split(decoded, []byte{0})
	if len(parts) != 3 || len(parts[0]) != 0 || len(parts[1]) == 0 || len(parts[2]) == 0 {
		return "", "", false
	}
	return string(parts[1]), string(parts[2]), true
}
