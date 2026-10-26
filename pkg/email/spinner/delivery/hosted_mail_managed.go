package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	protocol "github.com/webong/gateway/arranger/integration"
	"github.com/webong/gateway/pkg/email/spinner/plannerclient"
)

type HostedMailSpecResolver interface {
	Resolve(context.Context, string) (HostedMailAdapterSpec, error)
}

var ErrHostedMailAdapterUnavailable = errors.New("hosted mail adapter is disabled or missing")

type httpHostedMailResolver struct {
	origin string
	token  string
	client *http.Client
	prefix string
}

func NewHTTPHostedMailResolver(origin, token string) (*httpHostedMailResolver, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost"))) || token == "" {
		return nil, errors.New("private hosted mail resolver is invalid")
	}
	return &httpHostedMailResolver{origin: strings.TrimRight(origin, "/"), token: token,
		client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		prefix: "/_internal/gateway/email/hosted-adapters/"}, nil
}

func NewPlannerHostedMailResolver(handler http.Handler) (*httpHostedMailResolver, error) {
	client, err := plannerclient.New(handler)
	if err != nil {
		return nil, err
	}
	return &httpHostedMailResolver{origin: plannerclient.Origin, token: "extension-scoped", client: client,
		prefix: "/_internal/gateway/extensions/webong/email/hosted-adapters/"}, nil
}

func (r *httpHostedMailResolver) Resolve(ctx context.Context, name string) (HostedMailAdapterSpec, error) {
	if !validProviderAdapterName(name) || name == "hosted.mail" {
		return HostedMailAdapterSpec{}, errors.New("hosted mail adapter name is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		r.origin+r.prefix+name, nil)
	if err != nil {
		return HostedMailAdapterSpec{}, err
	}
	request.Header.Set("X-Gateway-Internal", r.token)
	response, err := r.client.Do(request)
	if err != nil {
		return HostedMailAdapterSpec{}, errors.New("hosted mail authority is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return HostedMailAdapterSpec{}, ErrHostedMailAdapterUnavailable
	}
	if response.StatusCode != http.StatusOK {
		return HostedMailAdapterSpec{}, fmt.Errorf("hosted mail authority returned status %d", response.StatusCode)
	}
	var spec HostedMailAdapterSpec
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16385))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil || decoder.Decode(new(any)) != io.EOF {
		return HostedMailAdapterSpec{}, errors.New("hosted mail spec is invalid")
	}
	return spec, nil
}

type managedHostedMailAdapter struct {
	resolver HostedMailSpecResolver
	allowed  map[string]struct{}
	client   *http.Client
}

func NewManagedHostedMailAdapter(resolver HostedMailSpecResolver, allowedHosts []string, client *http.Client) (*managedHostedMailAdapter, error) {
	if resolver == nil || len(allowedHosts) == 0 {
		return nil, errors.New("hosted mail requires a resolver and deployed allowed hosts")
	}
	allowed := make(map[string]struct{}, len(allowedHosts))
	for _, host := range allowedHosts {
		if host == "" || host != strings.ToLower(host) || strings.ContainsAny(host, ":/ \t\r\n") {
			return nil, errors.New("invalid hosted mail allowed host")
		}
		allowed[host] = struct{}{}
	}
	return &managedHostedMailAdapter{resolver: resolver, allowed: allowed, client: client}, nil
}

func (a *managedHostedMailAdapter) DeliverGateway(ctx context.Context, delivery protocol.GatewayDelivery) error {
	if a == nil || delivery.Protocol != protocol.ProtocolSMTP || delivery.AdapterName() != "hosted.mail" {
		return fmt.Errorf("%w: invalid managed hosted mail delivery", protocol.ErrPermanentDelivery)
	}
	name := delivery.Attributes["hosted_adapter_id"]
	if !validProviderAdapterName(name) || name == "hosted.mail" {
		return fmt.Errorf("%w: invalid hosted mail adapter", protocol.ErrPermanentDelivery)
	}
	spec, err := a.resolver.Resolve(ctx, name)
	if err != nil {
		if errors.Is(err, ErrHostedMailAdapterUnavailable) {
			return fmt.Errorf("%w: hosted mail adapter is unavailable", protocol.ErrPermanentDelivery)
		}
		return errors.New("hosted mail adapter lookup is unavailable")
	}
	parsed, err := url.Parse(spec.URL)
	if err != nil {
		return fmt.Errorf("%w: invalid hosted mail endpoint", protocol.ErrPermanentDelivery)
	}
	if _, allowed := a.allowed[parsed.Hostname()]; !allowed {
		return fmt.Errorf("%w: hosted mail endpoint is not deployed", protocol.ErrPermanentDelivery)
	}
	adapter, err := NewHostedMailHTTPAdapter(spec, a.client)
	if err != nil {
		return fmt.Errorf("%w: hosted mail adapter is invalid", protocol.ErrPermanentDelivery)
	}
	return adapter.DeliverGateway(ctx, delivery)
}

var _ protocol.GatewayDeliveryAdapter = (*managedHostedMailAdapter)(nil)

func validProviderAdapterName(name string) bool {
	return protocol.ValidAdapterName(name) && !strings.HasPrefix(name, "gateway.") && name != "mailbox.smtp"
}
