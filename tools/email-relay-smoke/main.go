// email-relay-smoke is a loopback SMTP relay fixture for end-to-end tests.
package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"strings"

	"github.com/emersion/go-sasl"
	smtp "github.com/emersion/go-smtp"
	"github.com/miekg/dns"
)

type message struct {
	From       string   `json:"from"`
	Recipients []string `json:"recipients"`
	Data       string   `json:"data"`
}

type backend struct{ output string }

func (b *backend) NewSession(conn *smtp.Conn) (smtp.Session, error) {
	return &session{backend: b, conn: conn}, nil
}

type session struct {
	backend    *backend
	conn       *smtp.Conn
	from       string
	recipients []string
}

func (*session) AuthMechanisms() []string         { return nil }
func (*session) Auth(string) (sasl.Server, error) { return nil, smtp.ErrAuthUnsupported }
func (s *session) Mail(from string, _ *smtp.MailOptions) error {
	s.from = from
	s.recipients = nil
	return nil
}
func (s *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	if strings.EqualFold(to, "refuse@example.net") {
		return &smtp.SMTPError{Code: 550, Message: "recipient refused by test relay"}
	}
	s.recipients = append(s.recipients, to)
	return nil
}
func (s *session) Data(reader io.Reader) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(s.backend.output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(message{From: s.from, Recipients: s.recipients, Data: string(data)}); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if strings.Contains(string(data), "Subject: gateway-smoke-drop-ack\r\n") {
		// The relay has accepted and persisted the message, but Spinner cannot
		// tell because its SMTP connection closes before the DATA response.
		return s.conn.Close()
	}
	return nil
}
func (s *session) Reset()        { s.from = ""; s.recipients = nil }
func (s *session) Logout() error { return nil }

func main() {
	address := flag.String("addr", "127.0.0.1:12527", "loopback SMTP listen address")
	dnsAddress := flag.String("dns-addr", "", "optional loopback DNS fixture listen address")
	output := flag.String("output", "", "accepted-message JSON lines file")
	flag.Parse()
	if *output == "" {
		log.Fatal("-output is required")
	}
	if *dnsAddress != "" {
		packet, err := net.ListenPacket("udp", *dnsAddress)
		if err != nil {
			log.Fatal(err)
		}
		fixture := &dns.Server{PacketConn: packet, Handler: dns.HandlerFunc(func(writer dns.ResponseWriter, query *dns.Msg) {
			response := new(dns.Msg)
			response.SetReply(query)
			if len(query.Question) == 1 && query.Question[0].Name == "missing.example.test." {
				response.Rcode = dns.RcodeNameError
			}
			if len(query.Question) == 1 {
				log.Printf("DNS fixture query %s rcode=%d", query.Question[0].Name, response.Rcode)
			}
			_ = writer.WriteMsg(response)
		})}
		go func() {
			if err := fixture.ActivateAndServe(); err != nil {
				log.Printf("DNS fixture stopped: %v", err)
			}
		}()
	}
	server := smtp.NewServer(&backend{output: *output})
	server.Addr = *address
	server.Domain = "relay.example.test"
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
