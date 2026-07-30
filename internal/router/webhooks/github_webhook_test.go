package webhooks_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/go-github/v89/github"
	"github.com/gummicube/gc-wizard/internal/router"
)

// wizardUsername mirrors the hardcoded constant in github_webhooks.go.
const wizardUsername = "imdol"

// sign computes the X-Hub-Signature-256 value GitHub would send for payload,
// using whatever secret the running process resolved GITHUB_WEBHOOK_SECRET to.
func sign(secret, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func postWebhook(t *testing.T, eventType string, payload []byte, secret []byte) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Github-Event", eventType)
	req.Header.Set("X-Hub-Signature-256", sign(secret, payload))

	rec := httptest.NewRecorder()
	router.WizardHandler().ServeHTTP(rec, req)
	return rec
}

func TestGithubWebhook_IssueAssignedToWizard(t *testing.T) {
	secret := []byte(os.Getenv("GITHUB_WEBHOOK_SECRET"))

	// Mock request mimicking GitHub's "issues" webhook with action "assigned".
	event := &github.IssuesEvent{
		Action: new("assigned"),
		Issue: &github.Issue{
			Number: new(42),
			Title:  new("Something is broken"),
			Body:   new("Steps to reproduce..."),
		},
		Assignee: &github.User{
			Login: new(wizardUsername),
		},
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
    t.Logf("THE PAYLOAD EVENTS ISSUE: %v", payload)


	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	rec := postWebhook(t, "issues", payload, secret)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), "issue #42 assigned to imdol") {
		t.Errorf("expected handler to log the assignment, got: %s", logs.String())
	}
}

func TestGithubWebhook_IssueAssignedToOtherUser(t *testing.T) {
	secret := []byte(os.Getenv("GITHUB_WEBHOOK_SECRET"))

	event := &github.IssuesEvent{
		Action: new("assigned"),
		Issue: &github.Issue{
			Number: new(7),
		},
		Assignee: &github.User{
			Login: new("someone-else"),
		},
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	rec := postWebhook(t, "issues", payload, secret)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(logs.String(), "assigned to imdol") {
		t.Errorf("handler should not act on issues assigned to other users, got: %s", logs.String())
	}
}

func TestGithubWebhook_InvalidSignature(t *testing.T) {
	secret := []byte(os.Getenv("GITHUB_WEBHOOK_SECRET"))
	wrongSecret := append([]byte("wrong-"), secret...)
	event := &github.IssuesEvent{
		Action: new("assigned"),
		Issue: &github.Issue{
			Number: new(1),
		},
		Assignee: &github.User{
			Login: new(wizardUsername),
		},
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	rec := postWebhook(t, "issues", payload, wrongSecret)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad signature, got %d: %s", rec.Code, rec.Body.String())
	}
}
