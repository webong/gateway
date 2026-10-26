// Package mailpoll executes Planner-owned IMAP jobs claimed by Gateway schedules.
package mailpoll

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"sort"
	"strconv"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"
)

const maxMessageBytes int64 = 10 << 20
const maxMessagesPerRun = 3

type InitialSync string

const (
	InitialSyncNew InitialSync = "new"
	InitialSyncAll InitialSync = "all"
)

type ClaimAction string

const (
	ClaimPoll     ClaimAction = "poll"
	ClaimBaseline ClaimAction = "baseline"
)

type Cursor struct {
	UIDValidity uint32 `json:"uid_validity"`
	LastUID     uint32 `json:"last_uid"`
}

type Claim struct {
	RouteID              string      `json:"route_id"`
	ConnectionID         string      `json:"connection_id"`
	Address              string      `json:"address"`
	CallbackURL          string      `json:"callback_url"`
	WebhookSecret        string      `json:"webhook_secret"`
	UpstreamAddress      string      `json:"upstream_address"`
	UpstreamServerName   string      `json:"upstream_server_name"`
	UpstreamNetworkScope string      `json:"upstream_network_scope"`
	Username             string      `json:"username"`
	Secret               string      `json:"secret"`
	Mechanism            string      `json:"mechanism"`
	Cursor               Cursor      `json:"cursor"`
	InitialSync          InitialSync `json:"initial_sync"`
	BaselinePending      bool        `json:"baseline_pending"`
	LeaseToken           string      `json:"lease_token"`
}

type ControlPlane interface {
	Claim(context.Context, string, ClaimAction) (Claim, error)
	Ack(context.Context, string, string, uint32, uint32) error
	Baseline(context.Context, string, string, uint32, uint32) error
	Release(context.Context, string, string) error
}

type DialFunc func(context.Context, Claim) (net.Conn, error)
type DeliverFunc func(context.Context, string, http.Header, []byte) (int, error)

type Runner struct {
	Control ControlPlane
	Dial    DialFunc
	Deliver DeliverFunc
}

// Run finishes within the scheduler's two-minute claim lease. A failed callback
// leaves the UID untouched; the next scheduled run retries the same event ID.
func (r Runner) Run(ctx context.Context, routeID string) error {
	return r.run(ctx, routeID, ClaimPoll)
}

// Baseline establishes the current mailbox UID position without delivering
// existing messages. Planner only grants this action for a paused, explicitly
// reset poll whose baseline is pending.
func (r Runner) Baseline(ctx context.Context, routeID string) error {
	return r.run(ctx, routeID, ClaimBaseline)
}

func (r Runner) run(ctx context.Context, routeID string, action ClaimAction) error {
	if r.Control == nil || r.Dial == nil || (action == ClaimPoll && r.Deliver == nil) {
		return errors.New("IMAP poll runner is incomplete")
	}
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	claim, err := r.Control.Claim(ctx, routeID, action)
	if errors.Is(err, ErrUnavailable) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer releaseCancel()
		_ = r.Control.Release(releaseCtx, routeID, claim.LeaseToken)
	}()
	if claim.RouteID != routeID || claim.LeaseToken == "" || claim.Address == "" {
		return errors.New("invalid IMAP poll claim")
	}
	connection, err := r.Dial(ctx, claim)
	if err != nil {
		return fmt.Errorf("dial IMAP upstream: %w", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(100 * time.Second))
	client := imapclient.New(connection, nil)
	defer client.Close()
	if claim.Mechanism == "PLAIN" {
		if err := client.Login(claim.Username, claim.Secret).Wait(); err != nil {
			return fmt.Errorf("IMAP login: %w", err)
		}
	} else if claim.Mechanism == "XOAUTH2" {
		if err := client.Authenticate(xoauth2{username: claim.Username, token: claim.Secret}); err != nil {
			return fmt.Errorf("IMAP OAuth: %w", err)
		}
	} else {
		return errors.New("unsupported IMAP authentication mechanism")
	}
	selected, err := client.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return fmt.Errorf("select INBOX: %w", err)
	}
	if selected.UIDValidity == 0 {
		return errors.New("IMAP server did not provide UIDVALIDITY")
	}
	if action == ClaimBaseline && !claim.BaselinePending {
		return errors.New("IMAP baseline was not explicitly requested")
	}
	initialSync := claim.InitialSync
	if initialSync == "" {
		initialSync = InitialSyncNew
	}
	if initialSync != InitialSyncNew && initialSync != InitialSyncAll {
		return errors.New("unsupported IMAP initial sync mode")
	}
	needsBaseline := action == ClaimBaseline || claim.Cursor.UIDValidity != selected.UIDValidity
	if action == ClaimPoll && claim.Cursor.UIDValidity == 0 && initialSync == InitialSyncAll {
		needsBaseline = false
	}
	if needsBaseline {
		if selected.UIDNext == 0 {
			return errors.New("IMAP server did not provide UIDNEXT")
		}
		lastUID := uint32(selected.UIDNext - 1)
		if err := r.Control.Baseline(ctx, routeID, claim.LeaseToken, selected.UIDValidity, lastUID); err != nil {
			return fmt.Errorf("baseline IMAP cursor: %w", err)
		}
		return nil
	}
	lastUID := claim.Cursor.LastUID
	if lastUID == ^uint32(0) {
		return nil
	}
	uidSet := imap.UIDSet{}
	uidSet.AddRange(imap.UID(lastUID+1), 0)
	search, err := client.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{uidSet}}, nil).Wait()
	if err != nil {
		return fmt.Errorf("search IMAP UIDs: %w", err)
	}
	uids := search.AllUIDs()
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	// IMAP interprets a range beyond the current highest UID with a
	// bidirectional wildcard; explicitly filter already acknowledged UIDs.
	uids = uids[:len(uids):len(uids)]
	pending := make([]imap.UID, 0, len(uids))
	for _, uid := range uids {
		if uint32(uid) > lastUID {
			pending = append(pending, uid)
		}
	}
	uids = pending
	if len(uids) > maxMessagesPerRun {
		uids = uids[:maxMessagesPerRun]
	}
	for _, uid := range uids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		body, found, err := fetchBody(client, uid)
		if err != nil {
			return err
		}
		if found {
			if err := r.deliver(ctx, claim, selected.UIDValidity, uint32(uid), body); err != nil {
				return err
			}
		}
		if err := r.Control.Ack(ctx, routeID, claim.LeaseToken, selected.UIDValidity, uint32(uid)); err != nil {
			return fmt.Errorf("acknowledge IMAP UID: %w", err)
		}
	}
	return nil
}

func fetchBody(client *imapclient.Client, uid imap.UID) ([]byte, bool, error) {
	section := &imap.FetchItemBodySection{Peek: true}
	cmd := client.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}})
	message := cmd.Next()
	if message == nil {
		if err := cmd.Close(); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	var body []byte
	for item := message.Next(); item != nil; item = message.Next() {
		if part, ok := item.(imapclient.FetchItemDataBodySection); ok && part.Literal != nil {
			var err error
			body, err = io.ReadAll(io.LimitReader(part.Literal, maxMessageBytes+1))
			if err != nil {
				return nil, false, err
			}
			if int64(len(body)) > maxMessageBytes {
				return nil, false, errors.New("IMAP message exceeds size limit")
			}
		}
	}
	if err := cmd.Close(); err != nil {
		return nil, false, err
	}
	if body == nil {
		return nil, false, errors.New("IMAP FETCH omitted message body")
	}
	return body, true, nil
}

func (r Runner) deliver(ctx context.Context, claim Claim, validity, uid uint32, body []byte) error {
	eventSum := sha256.Sum256([]byte(claim.RouteID + "\n" + strconv.FormatUint(uint64(validity), 10) + "\n" + strconv.FormatUint(uint64(uid), 10)))
	eventID := hex.EncodeToString(eventSum[:])
	mailFrom := ""
	if parsed, err := mail.ReadMessage(bytes.NewReader(body)); err == nil {
		if sender, err := mail.ParseAddress(parsed.Header.Get("From")); err == nil {
			mailFrom = sender.Address
		}
	}
	envelope, err := json.Marshal(map[string]any{"mail_from": mailFrom, "recipients": []string{claim.Address}})
	if err != nil {
		return err
	}
	envelopeHeader := base64.StdEncoding.EncodeToString(envelope)
	messageHash := sha256.Sum256(body)
	messageHex := hex.EncodeToString(messageHash[:])
	headers := http.Header{}
	headers.Set("Content-Type", "message/rfc822")
	headers.Set("X-Gateway-Route-Id", claim.RouteID)
	headers.Set("X-Gateway-Event-Id", eventID)
	headers.Set("X-Gateway-Envelope", envelopeHeader)
	headers.Set("X-Gateway-Signature", Sign(claim.RouteID, eventID, envelopeHeader, messageHex, claim.WebhookSecret))
	status, err := r.Deliver(ctx, claim.CallbackURL, headers, body)
	if err != nil {
		return fmt.Errorf("deliver IMAP callback: %w", err)
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("IMAP callback returned HTTP %d", status)
	}
	return nil
}

func Sign(routeID, eventID, envelopeHeader, messageSHA256, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = io.WriteString(mac, routeID+"\n"+eventID+"\n"+envelopeHeader+"\n"+messageSHA256)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

type xoauth2 struct{ username, token string }

var _ sasl.Client = xoauth2{}

func (x xoauth2) Start() (string, []byte, error) {
	return "XOAUTH2", []byte("user=" + x.username + "\x01auth=Bearer " + x.token + "\x01\x01"), nil
}
func (x xoauth2) Next([]byte) ([]byte, error) { return nil, sasl.ErrUnexpectedServerChallenge }
