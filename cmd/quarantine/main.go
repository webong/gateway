// email-quarantine is local operator tooling for a restricted SMTP quarantine.
// It does not expose quarantined content through the public Gateway API.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/webong/gateway/pkg/email/spinner/smtp"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: email-quarantine list|show|export|delete -path DIRECTORY [-id ID] [-output FILE]")
	}
	action := args[0]
	flags := flag.NewFlagSet(action, flag.ContinueOnError)
	path := flags.String("path", "", "existing private quarantine directory")
	id := flags.String("id", "", "opaque quarantine ID")
	output := flags.String("output", "", "new file for exported RFC 5322 bytes")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	quarantine, err := smtp.OpenFileQuarantine(*path)
	if err != nil {
		return err
	}
	switch action {
	case "list":
		records, err := quarantine.List()
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(records)
	case "show":
		record, _, err := quarantine.Read(*id)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(record)
	case "export":
		if !filepath.IsAbs(*output) || filepath.Clean(*output) == string(filepath.Separator) {
			return errors.New("export output must be an absolute new file path")
		}
		_, payload, err := quarantine.Read(*id)
		if err != nil {
			return err
		}
		file, err := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if _, err := file.Write(payload); err != nil {
			file.Close()
			os.Remove(*output)
			return err
		}
		if err := file.Sync(); err != nil {
			file.Close()
			os.Remove(*output)
			return err
		}
		return file.Close()
	case "delete":
		return quarantine.Delete(*id)
	default:
		return errors.New("usage: email-quarantine list|show|export|delete -path DIRECTORY [-id ID] [-output FILE]")
	}
}
