package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	smtpclient "github.com/emersion/go-smtp"
)

func main() {
	address := flag.String("addr", "127.0.0.1:2525", "SMTP server address")
	from := flag.String("from", "sender@example.test", "envelope sender")
	to := flag.String("to", "recipient@example.test", "comma-separated envelope recipients")
	subject := flag.String("subject", "gateway SMTP smoke", "message subject")
	messageFile := flag.String("message-file", "", "raw message file, or - for standard input")
	expectDataError := flag.Bool("expect-data-error", false, "expect the gateway to reject DATA")
	expectDataCode := flag.Int("expect-data-code", 0, "expected SMTP DATA error code")
	expectRcptCode := flag.Int("expect-rcpt-code", 0, "expected SMTP RCPT error code")
	flag.Parse()

	client, err := smtpclient.Dial(*address)
	if err != nil {
		fatal(err)
	}
	defer client.Close()

	if err := client.Hello("smtp-smoke-client.example.test"); err != nil {
		fatal(err)
	}
	if err := client.Mail(*from, nil); err != nil {
		fatal(err)
	}
	for _, recipient := range strings.Split(*to, ",") {
		if err := client.Rcpt(strings.TrimSpace(recipient), nil); err != nil {
			if *expectRcptCode != 0 {
				var smtpErr *smtpclient.SMTPError
				if errors.As(err, &smtpErr) && smtpErr.Code == *expectRcptCode {
					return
				}
				fatal(fmt.Errorf("expected RCPT status %d, got %v", *expectRcptCode, err))
			}
			fatal(err)
		}
	}
	if *expectRcptCode != 0 {
		fatal(fmt.Errorf("expected RCPT status %d but all recipients were accepted", *expectRcptCode))
	}

	data, err := client.Data()
	if err != nil {
		fatal(err)
	}
	message := []byte("Subject: " + *subject + "\r\n\r\nmessage from SMTP\r\n")
	if *messageFile != "" {
		var readErr error
		if *messageFile == "-" {
			message, readErr = io.ReadAll(os.Stdin)
		} else {
			message, readErr = os.ReadFile(*messageFile)
		}
		if readErr != nil {
			fatal(fmt.Errorf("read SMTP message: %v", readErr))
		}
		if len(message) == 0 {
			fatal(fmt.Errorf("SMTP message is empty"))
		}
	}
	if _, err := data.Write(message); err != nil {
		fatal(err)
	}
	if err := data.Close(); *expectDataError {
		if err == nil {
			fatal(fmt.Errorf("expected DATA to be rejected"))
		}
		if *expectDataCode != 0 {
			var smtpErr *smtpclient.SMTPError
			if !errors.As(err, &smtpErr) || smtpErr.Code != *expectDataCode {
				fatal(fmt.Errorf("expected DATA status %d, got %v", *expectDataCode, err))
			}
		}
		return
	} else if err != nil {
		fatal(err)
	}
	if err := client.Quit(); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
