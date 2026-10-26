package transport

import (
	"fmt"
	"time"

	smtpgateway "github.com/webong/gateway/pkg/email/spinner/smtp"
	relayconfig "github.com/webong/gateway/pkg/spinner/internal/config"
)

func NewEmailProtection(config *relayconfig.Config) (smtpgateway.MalwareScanner, smtpgateway.QuarantineStore, error) {
	if config == nil {
		return nil, nil, fmt.Errorf("email protection configuration is required")
	}
	if config.EmailQuarantineRetention < 0 || (config.EmailQuarantineRetention > 0 && config.EmailQuarantinePath == "") {
		return nil, nil, fmt.Errorf("GATEWAY_EMAIL_QUARANTINE_RETENTION requires GATEWAY_EMAIL_QUARANTINE_PATH and cannot be negative")
	}
	var scanner smtpgateway.MalwareScanner
	if config.EmailClamdAddress != "" {
		network := config.EmailClamdNetwork
		if network == "" {
			network = "tcp"
		}
		timeout := config.EmailClamdTimeout
		if timeout == 0 {
			timeout = 10 * time.Second
		}
		var err error
		scanner, err = smtpgateway.NewClamdScanner(network, config.EmailClamdAddress, timeout)
		if err != nil {
			return nil, nil, err
		}
	}
	if config.EmailQuarantinePath == "" {
		return scanner, nil, nil
	}
	quarantine, err := smtpgateway.NewFileQuarantine(config.EmailQuarantinePath)
	if err != nil {
		return nil, nil, err
	}
	return scanner, quarantine, nil
}
