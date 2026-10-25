package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	smtpclient "github.com/emersion/go-smtp"
	protocol "github.com/webong/gateway/arranger/integration"
	"github.com/webong/gateway/pkg/email/spinner/smtpout"
)

type OutboundSMTPTransport struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Address    string `json:"address"`
	ServerName string `json:"server_name"`
	LocalName  string `json:"local_name"`
	TLSMode    string `json:"tls_mode"`
	Username   string `json:"username"`
	Password   string `json:"password"`
}

var ErrOutboundCapacity = errors.New("outbound transport capacity is unavailable")

type ManagedSMTPAdapter struct {
	origin  string
	token   string
	timeout time.Duration
	signer  smtpout.DKIMSigning
	client  *http.Client
	send    func(context.Context, OutboundSMTPTransport, protocol.GatewayDelivery) error
}

func NewManagedSMTPAdapter(origin, token string, timeout time.Duration, signer smtpout.DKIMSigning) (*ManagedSMTPAdapter, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost"))) || token == "" {
		return nil, errors.New("private outbound transport resolver is invalid")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &ManagedSMTPAdapter{origin: strings.TrimRight(origin, "/"), token: token, timeout: timeout,
		signer: signer, client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (a *ManagedSMTPAdapter) call(ctx context.Context, path string, body any, result any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.origin+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Gateway-Internal", a.token)
	response, err := a.client.Do(request)
	if err != nil {
		return errors.New("outbound transport authority is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusTooManyRequests {
			return ErrOutboundCapacity
		}
		return fmt.Errorf("outbound transport authority returned status %d", response.StatusCode)
	}
	if result == nil {
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 32769))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("outbound transport authority returned invalid data")
	}
	return nil
}

func (a *ManagedSMTPAdapter) DeliverGateway(ctx context.Context, delivery protocol.GatewayDelivery) error {
	if a == nil || delivery.Protocol != protocol.ProtocolSMTP || delivery.AdapterName() != "managed.smtp" {
		return fmt.Errorf("%w: invalid managed SMTP delivery", protocol.ErrPermanentDelivery)
	}
	attr := delivery.Attributes
	id, pool, account, route, domain := attr["gateway_delivery_id"], attr["outbound_pool_id"], attr["account_id"], attr["outbound_route_id"], attr["outbound_domain_id"]
	senderDomain := attr["outbound_sender_domain"]
	mailFrom := attr["mail_from"]
	at := strings.LastIndexByte(mailFrom, '@')
	if id == "" || pool == "" || account == "" || route == "" || at < 1 || !strings.EqualFold(senderDomain, mailFrom[at+1:]) {
		return fmt.Errorf("%w: invalid managed SMTP sender binding", protocol.ErrPermanentDelivery)
	}
	var plan struct {
		Transports []OutboundSMTPTransport `json:"transports"`
	}
	if err := a.call(ctx, "/_internal/gateway/email/outbound-plan", map[string]string{
		"pool_id": pool, "account_id": account, "route_id": route, "domain_id": domain,
		"sender_domain": senderDomain, "delivery_id": id,
	}, &plan); err != nil {
		return err
	}
	if len(plan.Transports) == 0 || len(plan.Transports) > 32 {
		return errors.New("no eligible outbound transport")
	}
	var last error
	attemptIndex, _ := strconv.Atoi(attr["gateway_attempt_index"])
	for index, spec := range plan.Transports {
		if spec.Type != "smtp" || spec.ID == "" || spec.TLSMode == "none" {
			return fmt.Errorf("%w: invalid outbound transport", protocol.ErrPermanentDelivery)
		}
		reserveErr := a.call(ctx, "/_internal/gateway/email/outbound-attempts/"+id+"/reserve",
			map[string]any{"transport_id": spec.ID, "attempt_index": attemptIndex*32 + index}, nil)
		if errors.Is(reserveErr, ErrOutboundCapacity) {
			last = reserveErr
			continue
		}
		if reserveErr != nil {
			return reserveErr
		}
		if err := a.record(ctx, id, spec.ID, "attempted"); err != nil {
			return err
		}
		send := a.send
		if send == nil {
			send = a.sendSMTP
		}
		err := send(ctx, spec, delivery)
		if err == nil {
			_ = a.record(ctx, id, spec.ID, "accepted") // Relay accepted DATA; a lost status update must not cause a duplicate.
			return nil
		}
		if errors.Is(err, protocol.ErrUncertainDelivery) {
			_ = a.record(ctx, id, spec.ID, "ambiguous")
			return err
		}
		if errors.Is(err, protocol.ErrPermanentDelivery) {
			_ = a.record(ctx, id, spec.ID, "rejected")
			return err
		}
		last = err
		_ = a.record(ctx, id, spec.ID, "temporarily_delayed")
		if !safeSMTPFailover(err) {
			return err
		}
	}
	if last == nil {
		return ErrOutboundCapacity
	}
	return last
}

func (a *ManagedSMTPAdapter) sendSMTP(ctx context.Context, spec OutboundSMTPTransport, delivery protocol.GatewayDelivery) error {
	sender, err := smtpout.NewSender(smtpout.Config{
		Address: spec.Address, LocalName: spec.LocalName, ServerName: spec.ServerName,
		Username: spec.Username, Password: spec.Password, TLSMode: smtpout.TLSMode(spec.TLSMode),
		Timeout: a.timeout, NetworkScope: "public",
	})
	if err != nil {
		return fmt.Errorf("%w: invalid outbound transport", protocol.ErrPermanentDelivery)
	}
	adapter, err := smtpout.NewGatewayAdapter(sender, a.signer)
	if err != nil {
		return err
	}
	return adapter.DeliverGateway(ctx, delivery)
}

func (a *ManagedSMTPAdapter) record(ctx context.Context, deliveryID, transportID, state string) error {
	return a.call(ctx, "/_internal/gateway/email/outbound-attempts/"+deliveryID,
		map[string]string{"transport_id": transportID, "state": state}, nil)
}

func safeSMTPFailover(err error) bool {
	if errors.Is(err, smtpout.ErrPreAcceptConnection) {
		return true
	}
	var reply *smtpclient.SMTPError
	if errors.As(err, &reply) {
		return reply.Code == 421 || reply.Code == 450 || reply.Code == 451 || reply.Code == 452
	}
	var network net.Error
	return errors.As(err, &network) && !errors.Is(err, protocol.ErrUncertainDelivery)
}

var _ protocol.GatewayDeliveryAdapter = (*ManagedSMTPAdapter)(nil)
