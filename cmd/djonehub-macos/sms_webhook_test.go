package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func webhookFixtureSMS() receivedSMS {
	return receivedSMS{Sender: "test-sender", Content: "Test verification code: 482913", Timestamp: time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)}
}

func waitWebhook(t *testing.T, w *smsWebhook, check func(smsWebhookStatus) bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if check(w.getStatus()) {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("webhook did not reach expected state: %+v", w.getStatus())
		case <-ticker.C:
		}
	}
}

func TestSMSWebhookRecoversPendingAndDeduplicatesAfterRestart(t *testing.T) {
	type request struct {
		Event                         smsWebhookEvent
		Auth, ID, Method, ContentType string
	}
	requests := make(chan request, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event smsWebhookEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- request{event, r.Header.Get("Authorization"), r.Header.Get("X-DJOneHub-Event-ID"), r.Method, r.Header.Get("Content-Type")}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "state.json")
	first, err := newSMSWebhook(server.URL, "test-token", path)
	if err != nil {
		t.Fatal(err)
	}
	app := &app{smsWebhook: first}
	item := webhookFixtureSMS()
	app.recordSMS(item.Sender, item.Content, item.Timestamp)
	app.recordSMS(item.Sender, item.Content, item.Timestamp)
	if first.getStatus().Pending != 1 {
		t.Fatal("duplicate SMS was queued more than once")
	}

	// Simulate process exit before any HTTP delivery, then restore its outbox.
	second, err := newSMSWebhook(server.URL, "test-token", path)
	if err != nil {
		t.Fatal(err)
	}
	second.start()
	waitWebhook(t, second, func(s smsWebhookStatus) bool { return s.Delivered == 1 && s.Pending == 0 })
	second.close()
	got := <-requests
	if got.Event.Event != "sms.received" || got.Event.SMS.Content != item.Content || got.Event.SMS.Sender != item.Sender || got.Event.SMS.Code != "482913" || !got.Event.SMS.Timestamp.Equal(item.Timestamp) || got.Event.ReceivedAt.IsZero() {
		t.Fatalf("unexpected forwarded SMS: %+v", got.Event)
	}
	if got.Auth != "Bearer test-token" || got.ID != got.Event.ID || got.ID != smsWebhookID(item) || got.Method != "POST" || got.ContentType != "application/json" {
		t.Fatalf("unexpected request headers: %+v", got)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), item.Content) || strings.Contains(string(data), "test-token") {
		t.Fatal("acknowledged SMS body or credentials must not remain in the ledger")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state permissions = %o", info.Mode().Perm())
	}
	third, err := newSMSWebhook(server.URL, "test-token", path)
	if err != nil {
		t.Fatal(err)
	}
	item.Timestamp = item.Timestamp.In(time.FixedZone("test", 8*3600))
	if err := third.enqueue([]receivedSMS{item}); err != nil {
		t.Fatal(err)
	}
	if third.getStatus().Pending != 0 || third.getStatus().Delivered != 1 {
		t.Fatal("acknowledged message was queued again after restart/timezone change")
	}
}

func TestSMSWebhookRetriesWithoutBlockingInbox(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var attempts atomic.Int32
	ids := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids <- r.Header.Get("X-DJOneHub-Event-ID")
		if attempts.Add(1) == 1 {
			close(started)
			<-release
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	webhook, err := newSMSWebhook(server.URL, "", filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	webhook.retryBase, webhook.interval = 10*time.Millisecond, 5*time.Millisecond
	webhook.start()
	defer webhook.close()
	app := &app{smsWebhook: webhook}
	item := webhookFixtureSMS()
	app.recordSMS(item.Sender, item.Content, item.Timestamp)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("delivery did not start")
	}
	inbox := httptest.NewRecorder()
	app.routes().ServeHTTP(inbox, httptest.NewRequest("GET", "/api/sms", nil))
	if inbox.Code != http.StatusOK || !strings.Contains(inbox.Body.String(), item.Content) {
		close(release)
		t.Fatal("webhook must not block inbox reads")
	}
	close(release)
	waitWebhook(t, webhook, func(s smsWebhookStatus) bool { return s.Delivered == 1 && s.Pending == 0 })
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
	if first, second := <-ids, <-ids; first == "" || first != second {
		t.Fatal("retry must retain the same event ID")
	}
}

func TestSMSWebhookDoesNotForwardDemoOrParseFailures(t *testing.T) {
	w, err := newSMSWebhook("http://127.0.0.1:1", "", filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := &app{demo: true, smsWebhook: w}
	item := webhookFixtureSMS()
	a.recordSMS(item.Sender, item.Content, item.Timestamp)
	if w.getStatus().Pending != 0 {
		t.Fatal("demo SMS must not be forwarded")
	}
	a.demo = false
	a.mergeSMS([]receivedSMS{{Sender: "PDU", Content: "[短信解析失败] invalid", Timestamp: time.Now()}})
	if w.getStatus().Pending != 0 {
		t.Fatal("PDU diagnostics must not be forwarded")
	}
	// A real alphanumeric sender named PDU is still a valid SMS.
	a.recordSMS("PDU", "real message", item.Timestamp)
	if w.getStatus().Pending != 1 {
		t.Fatal("valid alphanumeric sender was skipped")
	}
}

func TestSMSWebhookRequeuesAfterStorageFailureEvenIfCached(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := newSMSWebhook("http://127.0.0.1:1", "", filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	w.path = filepath.Join(blocker, "state.json")
	a := &app{smsWebhook: w}
	items := []receivedSMS{webhookFixtureSMS()}
	a.mergeSMS(items)
	if w.getStatus().Pending != 0 || w.canCleanup(items) {
		t.Fatal("failed save must not lose the module SMS")
	}
	w.path = filepath.Join(root, "state.json")
	newCount, _ := a.mergeSMS(items)
	if newCount != 0 || w.getStatus().Pending != 1 || !w.canCleanup(items) {
		t.Fatal("a cached SMS must be queued again after disk recovery")
	}
}

func TestSMSWebhookTimeoutAndRedirectDoNotLeakDestination(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	w, err := newSMSWebhook(source.URL, "", filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	event := smsWebhookEvent{ID: "test", SMS: webhookFixtureSMS()}
	if err := w.deliver(context.Background(), event); err == nil || redirected.Load() != 0 {
		t.Fatal("SMS must not be forwarded to a redirect target")
	}
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(80 * time.Millisecond) }))
	defer stalled.Close()
	w.endpoint, w.client.Timeout = stalled.URL+"?token=private-value", 20*time.Millisecond
	if err := w.deliver(context.Background(), event); err == nil || strings.Contains(err.Error(), "private-value") || strings.Contains(err.Error(), stalled.URL) {
		t.Fatalf("timeout error must not expose endpoint details: %v", err)
	}
}

func TestSMSWebhookDisabledAndInvalidConfiguration(t *testing.T) {
	t.Setenv("DJONEHUB_WEBHOOK_URL", "")
	w, err := newSMSWebhookFromEnv()
	if w != nil || err != nil || w.getStatus().Enabled {
		t.Fatal("empty URL must disable webhook")
	}
	for _, endpoint := range []string{"file:///tmp/test", "https://", "https://user:private@example.com/sms", "https://example.com/#fragment"} {
		if _, err := newSMSWebhook(endpoint, "", filepath.Join(t.TempDir(), "state.json")); err == nil {
			t.Fatal("invalid webhook configuration was accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("invalid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newSMSWebhook("https://example.com/sms", "", path); err == nil {
		t.Fatal("corrupt ledger must not be silently replaced and replay messages")
	}
}
