package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type smsWebhookEvent struct {
	ID         string      `json:"id"`
	Event      string      `json:"event"`
	ReceivedAt time.Time   `json:"received_at"`
	SMS        receivedSMS `json:"sms"`
}

type smsWebhookPending struct {
	Event       smsWebhookEvent `json:"event"`
	Attempts    int             `json:"attempts"`
	NextAttempt time.Time       `json:"next_attempt"`
}

type smsWebhookState struct {
	Pending   map[string]smsWebhookPending `json:"pending"`
	Delivered map[string]bool              `json:"delivered"`
}

type smsWebhookStatus struct {
	Enabled     bool      `json:"enabled"`
	Pending     int       `json:"pending"`
	Delivered   int       `json:"delivered"`
	LastSuccess time.Time `json:"last_success"`
	LastError   string    `json:"last_error"`
}

type smsWebhook struct {
	endpoint  string
	token     string
	path      string
	client    *http.Client
	mu        sync.Mutex
	state     smsWebhookState
	status    smsWebhookStatus
	wake      chan struct{}
	cancel    context.CancelFunc
	done      chan struct{}
	retryBase time.Duration
	interval  time.Duration
}

func newSMSWebhookFromEnv() (*smsWebhook, error) {
	endpoint := strings.TrimSpace(os.Getenv("DJONEHUB_WEBHOOK_URL"))
	if endpoint == "" {
		return nil, nil
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	w, err := newSMSWebhook(endpoint, os.Getenv("DJONEHUB_WEBHOOK_TOKEN"), filepath.Join(root, "DJOneHub", "sms-webhook.json"))
	if err == nil {
		w.start()
		log.Printf("SMS webhook enabled")
	}
	return w, err
}

func newSMSWebhook(endpoint, token, path string) (*smsWebhook, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("DJONEHUB_WEBHOOK_URL must be an HTTP(S) URL without user info or fragment")
	}
	if strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("DJONEHUB_WEBHOOK_TOKEN must not contain line breaks")
	}
	w := &smsWebhook{
		endpoint: endpoint, token: token, path: path,
		client: &http.Client{
			Timeout: 10 * time.Second,
			// Never forward SMS or credentials to an unexpected redirect target.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		state:  smsWebhookState{Pending: make(map[string]smsWebhookPending), Delivered: make(map[string]bool)},
		status: smsWebhookStatus{Enabled: true}, wake: make(chan struct{}, 1), done: make(chan struct{}),
		retryBase: 2 * time.Second, interval: time.Second,
	}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &w.state); err != nil || w.state.Pending == nil || w.state.Delivered == nil {
			return nil, errors.New("SMS webhook state is invalid; existing records were left unchanged")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read SMS webhook state: %w", err)
	}
	return w, nil
}

func (w *smsWebhook) start() {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	go w.run(ctx)
}

func (w *smsWebhook) close() {
	if w == nil || w.cancel == nil {
		return
	}
	w.cancel()
	<-w.done
	w.client.CloseIdleConnections()
}

func smsWebhookID(item receivedSMS) string {
	hash := sha256.Sum256([]byte(smsCacheKey(item)))
	return hex.EncodeToString(hash[:])
}

// Persist before notifying the worker. Repeated device polls also retry an
// enqueue that failed to save, even when the message is already in the UI cache.
func (w *smsWebhook) enqueue(messages []receivedSMS) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var added []string
	for _, item := range messages {
		// Failed PDU decoding creates a diagnostic item with a new timestamp on
		// each poll. It is not a received text message and must not be forwarded.
		if item.Sender == "PDU" && strings.HasPrefix(item.Content, "[短信解析失败]") {
			continue
		}
		id := smsWebhookID(item)
		if _, exists := w.state.Pending[id]; exists || w.state.Delivered[id] {
			continue
		}
		if item.Code == "" {
			item.Code = extractSMSCode(item.Content)
		}
		w.state.Pending[id] = smsWebhookPending{Event: smsWebhookEvent{
			ID: id, Event: "sms.received", ReceivedAt: time.Now().UTC(), SMS: item,
		}}
		added = append(added, id)
	}
	if len(added) == 0 {
		return nil
	}
	if err := w.saveLocked(); err != nil {
		for _, id := range added {
			delete(w.state.Pending, id)
		}
		w.status.LastError = "待转发短信保存失败"
		return err
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return nil
}

func (w *smsWebhook) getStatus() smsWebhookStatus {
	if w == nil {
		return smsWebhookStatus{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	status := w.status
	status.Pending, status.Delivered = len(w.state.Pending), len(w.state.Delivered)
	return status
}

// Keep module messages if the webhook could not durably queue them.
func (w *smsWebhook) canCleanup(messages []receivedSMS) bool {
	if w == nil {
		return true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, item := range messages {
		id := smsWebhookID(item)
		if _, ok := w.state.Pending[id]; !ok && !w.state.Delivered[id] {
			return false
		}
	}
	return true
}

func (w *smsWebhook) saveLocked() error {
	data, err := json.Marshal(w.state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(w.path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(w.path), ".sms-webhook-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), w.path)
}

func (w *smsWebhook) nextDue() (smsWebhookPending, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var next smsWebhookPending
	found := false
	for _, pending := range w.state.Pending {
		if pending.NextAttempt.After(time.Now()) {
			continue
		}
		if !found || pending.Event.ReceivedAt.Before(next.Event.ReceivedAt) {
			next, found = pending, true
		}
	}
	return next, found
}

func (w *smsWebhook) run(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		for ctx.Err() == nil {
			pending, ok := w.nextDue()
			if !ok {
				break
			}
			err := w.deliver(ctx, pending.Event)
			if ctx.Err() != nil {
				return // Pending record remains for the next process.
			}
			w.complete(pending, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-w.wake:
		}
	}
}

func (w *smsWebhook) deliver(ctx context.Context, event smsWebhookEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return errors.New("webhook payload encoding failed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("webhook request creation failed")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DJOneHub-Event-ID", event.ID)
	if w.token != "" {
		req.Header.Set("Authorization", "Bearer "+w.token)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		// net/http errors contain the URL, which can contain a private token.
		return errors.New("webhook HTTP request failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (w *smsWebhook) complete(pending smsWebhookPending, deliveryErr error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	id := pending.Event.ID
	if deliveryErr == nil {
		delete(w.state.Pending, id)
		w.state.Delivered[id] = true
		if err := w.saveLocked(); err != nil {
			delete(w.state.Delivered, id)
			pending.NextAttempt = time.Now().Add(w.retryBase)
			w.state.Pending[id] = pending
			w.status.LastError = "转发结果保存失败，将使用相同事件 ID 重试"
			log.Printf("SMS webhook delivery state could not be saved")
			return
		}
		w.status.LastSuccess, w.status.LastError = time.Now().UTC(), ""
		return
	}
	pending.Attempts++
	delay := min(w.retryBase*time.Duration(1<<min(pending.Attempts-1, 8)), 5*time.Minute)
	pending.NextAttempt = time.Now().Add(delay)
	w.state.Pending[id] = pending
	w.status.LastError = deliveryErr.Error()
	if err := w.saveLocked(); err != nil {
		w.status.LastError = "转发失败，重试记录保存失败"
	}
	log.Printf("SMS webhook delivery failed; retry scheduled in %s (%s)", delay, deliveryErr)
}
