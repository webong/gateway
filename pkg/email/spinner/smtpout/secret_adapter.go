package smtpout

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	protocol "github.com/webong/gateway/arranger/integration"
)

type secretRelayCredential struct {
	Username   string `json:"username"`
	Password   string `json:"password,omitempty"`
	OAuthToken string `json:"oauth_token,omitempty"`
}

// SecretGatewayAdapter resolves a current credential for every attempt, so a
// Planner rotation does not require restarting or republishing the Extension.
type SecretGatewayAdapter struct {
	base      Config
	reference string
	secrets   protocol.SecretResolver
	signer    DKIMSigning
}

func NewSecretGatewayAdapter(base Config, reference string, secrets protocol.SecretResolver, signer DKIMSigning) (*SecretGatewayAdapter, error) {
	if !protocol.ValidSecretReference(reference) || secrets == nil || base.Username != "" || base.Password != "" || base.OAuthToken != "" {
		return nil, errors.New("credentialed SMTP relay configuration is invalid")
	}
	if _, err := NewSender(base); err != nil {
		return nil, err
	}
	return &SecretGatewayAdapter{base: base, reference: reference, secrets: secrets, signer: signer}, nil
}

func (adapter *SecretGatewayAdapter) DeliverGateway(ctx context.Context, delivery protocol.GatewayDelivery) error {
	secret, err := adapter.secrets.Resolve(ctx, adapter.reference)
	if err != nil {
		return errors.New("SMTP relay credential is unavailable")
	}
	var credential secretRelayCredential
	decoder := json.NewDecoder(bytes.NewReader(secret.Value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&credential); err != nil || decoder.Decode(new(any)) != io.EOF ||
		credential.Username == "" || (credential.Password == "") == (credential.OAuthToken == "") {
		return errors.New("SMTP relay credential is invalid")
	}
	config := adapter.base
	config.Username, config.Password, config.OAuthToken = credential.Username, credential.Password, credential.OAuthToken
	sender, err := NewSender(config)
	if err != nil {
		return errors.New("SMTP relay credential is invalid")
	}
	gateway, err := NewGatewayAdapter(sender, adapter.signer)
	if err != nil {
		return err
	}
	return gateway.DeliverGateway(ctx, delivery)
}

var _ protocol.GatewayDeliveryAdapter = (*SecretGatewayAdapter)(nil)
