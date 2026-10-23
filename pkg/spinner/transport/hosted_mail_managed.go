package transport

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

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	"github.com/webong/gateway/pkg/spinner/internal/config"
)

type hostedMailSpecResolver interface {
	Resolve(context.Context, string) (config.HostedMailAdapterSpec, error)
}

var errHostedMailAdapterUnavailable = errors.New("hosted mail adapter is disabled or missing")

type httpHostedMailResolver struct {
	origin string
	token  string
	client *http.Client
}

func newHTTPHostedMailResolver(origin, token string) (*httpHostedMailResolver, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost"))) || token == "" {
		return nil, errors.New("private hosted mail resolver is invalid")
	}
	return &httpHostedMailResolver{origin: strings.TrimRight(origin, "/"), token: token,
		client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (r *httpHostedMailResolver) Resolve(ctx context.Context, name string) (config.HostedMailAdapterSpec, error) {
	if !validProviderAdapterName(name) || name == "hosted.mail" {
		return config.HostedMailAdapterSpec{}, errors.New("hosted mail adapter name is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		r.origin+"/_internal/gateway/email/hosted-adapters/"+name, nil)
	if err != nil {
		return config.HostedMailAdapterSpec{}, err
	}
	request.Header.Set("X-Gateway-Internal", r.token)
	response, err := r.client.Do(request)
	if err != nil {
		return config.HostedMailAdapterSpec{}, errors.New("hosted mail authority is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return config.HostedMailAdapterSpec{}, errHostedMailAdapterUnavailable
	}
	if response.StatusCode != http.StatusOK {
		return config.HostedMailAdapterSpec{}, fmt.Errorf("hosted mail authority returned status %d", response.StatusCode)
	}
	var spec config.HostedMailAdapterSpec
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16385))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil || decoder.Decode(new(any)) != io.EOF {
		return config.HostedMailAdapterSpec{}, errors.New("hosted mail spec is invalid")
	}
	return spec, nil
}

type managedHostedMailAdapter struct {
	resolver hostedMailSpecResolver
	allowed  map[string]struct{}
	client   *http.Client
}

func newManagedHostedMailAdapter(resolver hostedMailSpecResolver, allowedHosts []string, client *http.Client) (*managedHostedMailAdapter, error) {
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

func (a *managedHostedMailAdapter) DeliverGateway(ctx context.Context, delivery bridge.GatewayDelivery) error {
	if a == nil || delivery.Protocol != bridge.ProtocolSMTP || delivery.AdapterName() != "hosted.mail" {
		return fmt.Errorf("%w: invalid managed hosted mail delivery", bridge.ErrPermanentDelivery)
	}
	name := delivery.Attributes["hosted_adapter_id"]
	if !validProviderAdapterName(name) || name == "hosted.mail" {
		return fmt.Errorf("%w: invalid hosted mail adapter", bridge.ErrPermanentDelivery)
	}
	spec, err := a.resolver.Resolve(ctx, name)
	if err != nil {
		if errors.Is(err, errHostedMailAdapterUnavailable) {
			return fmt.Errorf("%w: hosted mail adapter is unavailable", bridge.ErrPermanentDelivery)
		}
		return errors.New("hosted mail adapter lookup is unavailable")
	}
	parsed, err := url.Parse(spec.URL)
	if err != nil {
		return fmt.Errorf("%w: invalid hosted mail endpoint", bridge.ErrPermanentDelivery)
	}
	if _, allowed := a.allowed[parsed.Hostname()]; !allowed {
		return fmt.Errorf("%w: hosted mail endpoint is not deployed", bridge.ErrPermanentDelivery)
	}
	adapter, err := NewHostedMailHTTPAdapter(spec, a.client)
	if err != nil {
		return fmt.Errorf("%w: hosted mail adapter is invalid", bridge.ErrPermanentDelivery)
	}
	return adapter.DeliverGateway(ctx, delivery)
}

var _ bridge.GatewayDeliveryAdapter = (*managedHostedMailAdapter)(nil)
