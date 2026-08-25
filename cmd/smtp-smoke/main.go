package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	smtpclient "github.com/emersion/go-smtp"
)

func main() {
	address := flag.String("addr", "127.0.0.1:2525", "SMTP server address")
	flag.Parse()

	client, err := smtpclient.Dial(*address)
	if err != nil {
		fatal(err)
	}
	defer client.Close()

	if err := client.Hello("smtp-smoke-client.example.test"); err != nil {
		fatal(err)
	}
	if err := client.Mail("sender@example.test", nil); err != nil {
		fatal(err)
	}
	if err := client.Rcpt("recipient@example.test", nil); err != nil {
		fatal(err)
	}

	data, err := client.Data()
	if err != nil {
		fatal(err)
	}
	if _, err := io.WriteString(data, "Subject: gateway SMTP smoke\r\n\r\nmessage from SMTP\r\n"); err != nil {
		fatal(err)
	}
	if err := data.Close(); err != nil {
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
