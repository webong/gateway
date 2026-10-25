// Package pop3 is a TLS-only mailbox retrieval gateway. Planner authenticates
// clients and selects an operator-registered upstream; Spinner never stores or
// interprets mailbox contents.
package pop3

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	bridge "github.com/webong/gateway/arranger/integration"
	"github.com/webong/gateway/pkg/email/spinner/mailnet"
)

var ErrServerClosed = errors.New("POP3 server closed")

type Upstream struct {
	Address    string `json:"address"`
	ServerName string `json:"server_name"`
}

type Config struct {
	Address             string
	Hostname            string
	TLSConfig           *tls.Config
	Upstreams           map[string]Upstream
	UpstreamRootCAs     *x509.CertPool
	DialManagedUpstream mailnet.MailTLSDialer
	MaxConnections      int
	AuthPerMinute       int
	AuthBurst           int
	IdleTimeout         time.Duration
	PlannerTimeout      time.Duration
	UpstreamTimeout     time.Duration
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
		return nil, errors.New("POP3 requires a TLS certificate and private key")
	}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load POP3 TLS certificate: %w", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}, nil
}

func NewServer(config Config, planner bridge.ProtocolPlanner) (*Server, error) {
	if config.Address == "" || config.TLSConfig == nil ||
		(len(config.TLSConfig.Certificates) == 0 && config.TLSConfig.GetCertificate == nil) || planner == nil {
		return nil, errors.New("POP3 listener requires an address, TLS certificate, and Planner")
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
		config.IdleTimeout = 10 * time.Minute
	}
	if config.PlannerTimeout == 0 {
		config.PlannerTimeout = 10 * time.Second
	}
	if config.UpstreamTimeout == 0 {
		config.UpstreamTimeout = 10 * time.Second
	}
	if config.DialManagedUpstream == nil {
		config.DialManagedUpstream = mailnet.DialTLSMailUpstream
	}
	if config.MaxConnections < 1 || config.AuthPerMinute < 1 || config.AuthBurst < 1 || config.IdleTimeout < 10*time.Minute || config.PlannerTimeout < 1 || config.UpstreamTimeout < 1 {
		return nil, errors.New("POP3 connection and timeout limits are invalid")
	}
	for id, upstream := range config.Upstreams {
		if !validUpstreamID(id) || upstream.Address == "" || upstream.ServerName == "" {
			return nil, fmt.Errorf("invalid POP3 upstream %q", id)
		}
		if _, _, err := net.SplitHostPort(upstream.Address); err != nil {
			return nil, fmt.Errorf("invalid POP3 upstream %q address: %w", id, err)
		}
	}
	config.TLSConfig = config.TLSConfig.Clone()
	config.TLSConfig.MinVersion = tls.VersionTLS12
	return &Server{
		config: config, planner: planner, slots: make(chan struct{}, config.MaxConnections),
		limiter: newAuthLimiter(config.AuthPerMinute, config.AuthBurst),
		stop:    make(chan struct{}), serveDone: make(chan struct{}), clients: make(map[net.Conn]struct{}),
	}, nil
}

func validUpstreamID(id string) bool {
	if len(id) == 0 || len(id) > 64 || id[0] < 'a' || id[0] > 'z' {
		return false
	}
	for _, char := range id[1:] {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
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
		return errors.New("POP3 listener is required")
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
		return errors.New("POP3 listener is already running")
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
	reader := bufio.NewReaderSize(connection, 512)
	if _, err := io.WriteString(connection, "+OK Gateway POP3 ready\r\n"); err != nil {
		return
	}
	sessionID := randomID()
	username := ""
	attempts := 0
	for {
		_ = connection.SetReadDeadline(time.Now().Add(s.config.IdleTimeout))
		line, err := readLine(reader, 255)
		if err != nil {
			return
		}
		command, arg, hasArg := strings.Cut(line, " ")
		switch strings.ToUpper(command) {
		case "CAPA":
			if _, err := io.WriteString(connection, "+OK capabilities\r\nUSER\r\n.\r\n"); err != nil {
				return
			}
		case "QUIT":
			_, _ = io.WriteString(connection, "+OK goodbye\r\n")
			return
		case "USER":
			if !hasArg || !validUsername(arg) {
				_, _ = io.WriteString(connection, "-ERR invalid username\r\n")
				continue
			}
			username = arg
			if _, err := io.WriteString(connection, "+OK username received\r\n"); err != nil {
				return
			}
		case "PASS":
			if username == "" || !hasArg || !validCredential(arg) {
				_, _ = io.WriteString(connection, "-ERR USER required before PASS\r\n")
				continue
			}
			if !s.limiter.allow(connection.RemoteAddr().String()) {
				_, _ = io.WriteString(connection, "-ERR authentication rate limit exceeded\r\n")
				return
			}
			attempts++
			grant, err := s.authenticate(sessionID, connection.RemoteAddr().String(), username, arg)
			if err != nil {
				_, _ = io.WriteString(connection, "-ERR mailbox service unavailable\r\n")
				return
			}
			if grant == nil {
				_, _ = io.WriteString(connection, "-ERR authentication failed\r\n")
				username = ""
				if attempts >= 3 {
					return
				}
				continue
			}
			upstream, upstreamReader, err := s.connectUpstream(grant)
			if err != nil {
				_, _ = io.WriteString(connection, "-ERR mailbox service unavailable\r\n")
				return
			}
			defer upstream.Close()
			if err := s.audit(sessionID, username, connection.RemoteAddr().String(), bridge.EventConnect, grant); err != nil {
				_, _ = io.WriteString(connection, "-ERR mailbox service unavailable\r\n")
				return
			}
			defer func() { _ = s.audit(sessionID, username, connection.RemoteAddr().String(), bridge.EventClose, grant) }()
			if _, err := io.WriteString(connection, "+OK mailbox ready\r\n"); err != nil {
				return
			}
			s.proxy(connection, reader, upstream, upstreamReader)
			return
		default:
			if _, err := io.WriteString(connection, "-ERR unsupported command before login\r\n"); err != nil {
				return
			}
		}
	}
}

type mailboxGrant struct {
	identity     string
	upstream     Upstream
	username     string
	password     string
	id           string
	networkScope string
}

func (s *Server) authenticate(sessionID, remoteAddr, username, password string) (*mailboxGrant, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.config.PlannerTimeout)
	defer cancel()
	decision, err := s.planner.PlanEvent(ctx, bridge.GatewayEvent{
		ID: randomID(), Protocol: bridge.ProtocolPOP3, Kind: bridge.EventAuthenticate,
		SessionID: sessionID, Host: s.config.Hostname, Route: username,
		Attributes: map[string]string{"username": username, "password": password, "remote_addr": remoteAddr},
	})
	if err != nil || decision.Validate() != nil || decision.Protocol != bridge.ProtocolPOP3 {
		return nil, errors.New("POP3 authentication planning failed")
	}
	if decision.Action != bridge.GatewayAccept {
		return nil, nil
	}
	id := decision.Metadata["upstream_id"]
	upstream, ok := s.config.Upstreams[id]
	if address := decision.Metadata["upstream_address"]; address != "" {
		name := decision.Metadata["upstream_server_name"]
		scope := decision.Metadata["upstream_network_scope"]
		if scope == "" {
			scope = "public"
		}
		if !mailnet.ValidMailUpstream(address, name, scope) {
			return nil, errors.New("POP3 upstream is invalid")
		}
		upstream, ok = Upstream{Address: address, ServerName: name}, true
		decision.Metadata["upstream_network_scope"] = scope
	}
	identity := decision.Metadata["identity"]
	upstreamUsername := decision.Metadata["upstream_username"]
	upstreamPassword := decision.Metadata["upstream_password"]
	if !ok || identity == "" || !validUsername(upstreamUsername) || !validCredential(upstreamPassword) || len(upstreamUsername) > 248 || len(upstreamPassword) > 248 {
		return nil, errors.New("POP3 authentication decision has invalid upstream credentials")
	}
	return &mailboxGrant{identity: identity, upstream: upstream, username: upstreamUsername, password: upstreamPassword, id: id,
		networkScope: decision.Metadata["upstream_network_scope"]}, nil
}

func (s *Server) connectUpstream(grant *mailboxGrant) (net.Conn, *bufio.Reader, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.config.UpstreamTimeout)
	defer cancel()
	var connection net.Conn
	var err error
	if grant.networkScope != "" {
		connection, err = s.config.DialManagedUpstream(ctx, grant.upstream.Address, grant.upstream.ServerName,
			grant.networkScope, s.config.UpstreamTimeout, s.config.UpstreamRootCAs)
	} else {
		connection, err = (&tls.Dialer{
			NetDialer: &net.Dialer{Timeout: s.config.UpstreamTimeout},
			Config:    &tls.Config{ServerName: grant.upstream.ServerName, RootCAs: s.config.UpstreamRootCAs, MinVersion: tls.VersionTLS12},
		}).DialContext(ctx, "tcp", grant.upstream.Address)
	}
	if err != nil {
		return nil, nil, err
	}
	_ = connection.SetDeadline(time.Now().Add(s.config.UpstreamTimeout))
	reader := bufio.NewReaderSize(connection, 512)
	greeting, err := readLine(reader, 512)
	if err != nil || !strings.HasPrefix(greeting, "+OK") {
		_ = connection.Close()
		return nil, nil, errors.New("POP3 upstream did not greet successfully")
	}
	for _, command := range []string{"USER " + grant.username, "PASS " + grant.password} {
		if _, err := io.WriteString(connection, command+"\r\n"); err != nil {
			_ = connection.Close()
			return nil, nil, err
		}
		response, err := readLine(reader, 512)
		if err != nil || !strings.HasPrefix(response, "+OK") {
			_ = connection.Close()
			return nil, nil, errors.New("POP3 upstream rejected mapped credentials")
		}
	}
	_ = connection.SetDeadline(time.Time{})
	return connection, reader, nil
}

func (s *Server) audit(sessionID, username, remoteAddr string, kind bridge.EventKind, grant *mailboxGrant) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.config.PlannerTimeout)
	defer cancel()
	decision, err := s.planner.PlanEvent(ctx, bridge.GatewayEvent{
		ID: randomID(), Protocol: bridge.ProtocolPOP3, Kind: kind,
		SessionID: sessionID, Host: s.config.Hostname, Route: username,
		Attributes: map[string]string{"identity": grant.identity, "upstream_id": grant.id, "remote_addr": remoteAddr},
	})
	if err != nil || decision.Validate() != nil || decision.Protocol != bridge.ProtocolPOP3 || decision.Action != bridge.GatewayAccept {
		return errors.New("POP3 session audit planning failed")
	}
	return nil
}

func (s *Server) proxy(client net.Conn, clientReader *bufio.Reader, upstream net.Conn, upstreamReader *bufio.Reader) {
	clientRead := &idleReader{Reader: clientReader, Conn: client, timeout: s.config.IdleTimeout}
	upstreamRead := &idleReader{Reader: upstreamReader, Conn: upstream, timeout: s.config.IdleTimeout}
	client = &idleConnection{Conn: client, timeout: s.config.IdleTimeout}
	upstream = &idleConnection{Conn: upstream, timeout: s.config.IdleTimeout}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, clientRead); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstreamRead); done <- struct{}{} }()
	<-done
	_ = client.Close()
	_ = upstream.Close()
	<-done
}

type idleConnection struct {
	net.Conn
	timeout time.Duration
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

func (c *idleConnection) Read(data []byte) (int, error) {
	_ = c.Conn.SetReadDeadline(time.Now().Add(c.timeout))
	return c.Conn.Read(data)
}

func (c *idleConnection) Write(data []byte) (int, error) {
	_ = c.Conn.SetWriteDeadline(time.Now().Add(c.timeout))
	return c.Conn.Write(data)
}

func readLine(reader *bufio.Reader, max int) (string, error) {
	line, err := reader.ReadSlice('\n')
	if err != nil || len(line) > max || !strings.HasSuffix(string(line), "\r\n") {
		return "", errors.New("POP3 line is invalid or too long")
	}
	return string(line[:len(line)-2]), nil
}

func validCredential(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char > 0x7e {
			return false
		}
	}
	return true
}

func validUsername(value string) bool {
	return validCredential(value) && !strings.Contains(value, " ")
}

func randomID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic("POP3 session entropy is unavailable")
	}
	return hex.EncodeToString(data[:])
}
