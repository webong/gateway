package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	"github.com/webong/gateway/pkg/spinner/internal/config"
)

// HostedMailHTTPAdapter submits raw MIME to one fixed, authenticated provider
// endpoint. Only the operator can set the endpoint or bearer token; a tenant
// route supplies a logical mailbox key and account ID.
type HostedMailHTTPAdapter struct {
	endpoint string
	token    string
	client   *http.Client
}

func NewHostedMailHTTPAdapter(spec config.HostedMailAdapterSpec, client *http.Client) (*HostedMailHTTPAdapter, error) {
	parsed, err := url.Parse(spec.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		spec.BearerToken == "" || len(spec.BearerToken) > 4096 || strings.IndexFunc(spec.BearerToken, func(r rune) bool { return r < '!' || r > '~' }) >= 0 {
		return nil, errors.New("hosted mail adapter requires a fixed HTTPS endpoint and bearer token")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HostedMailHTTPAdapter{endpoint: parsed.String(), token: spec.BearerToken, client: &copyClient}, nil
}

func (adapter *HostedMailHTTPAdapter) DeliverGateway(ctx context.Context, delivery bridge.GatewayDelivery) error {
	if adapter == nil || delivery.Protocol != bridge.ProtocolSMTP || delivery.Target == "" || len(delivery.Payload) == 0 ||
		delivery.SubscriberID == "" || delivery.Attributes["account_id"] != delivery.SubscriberID ||
		delivery.Attributes["recipient"] == "" || delivery.Attributes["event_id"] == "" {
		return fmt.Errorf("%w: hosted mailbox delivery is invalid", bridge.ErrPermanentDelivery)
	}
	envelope, err := json.Marshal(map[string]string{
		"account_id": delivery.SubscriberID, "mailbox_key": delivery.Target,
		"mail_from": delivery.Attributes["mail_from"], "recipient": delivery.Attributes["recipient"],
	})
	if err != nil {
		return fmt.Errorf("%w: hosted mailbox envelope is invalid", bridge.ErrPermanentDelivery)
	}
	digest := sha256.Sum256(append([]byte(delivery.Attributes["event_id"]+"\x00"+delivery.SubscriberID+"\x00"+delivery.Target+"\x00"+delivery.Attributes["recipient"]+"\x00"), delivery.Payload...))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, adapter.endpoint, bytes.NewReader(delivery.Payload))
	if err != nil {
		return fmt.Errorf("%w: hosted mailbox request is invalid", bridge.ErrPermanentDelivery)
	}
	request.Header.Set("Authorization", "Bearer "+adapter.token)
	request.Header.Set("Content-Type", "message/rfc822")
	request.Header.Set("Idempotency-Key", hex.EncodeToString(digest[:]))
	request.Header.Set("X-Gateway-Event-Id", delivery.Attributes["event_id"])
	request.Header.Set("X-Gateway-Envelope", base64.StdEncoding.EncodeToString(envelope))
	response, err := adapter.client.Do(request)
	if err != nil {
		return errors.New("hosted mailbox endpoint is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != 408 && response.StatusCode != 429 {
		return fmt.Errorf("%w: hosted mailbox endpoint rejected delivery with status %d", bridge.ErrPermanentDelivery, response.StatusCode)
	}
	return fmt.Errorf("hosted mailbox endpoint returned status %d", response.StatusCode)
}

var _ bridge.GatewayDeliveryAdapter = (*HostedMailHTTPAdapter)(nil)
