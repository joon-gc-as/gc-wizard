package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	reconnectDelay = 3 * time.Second
	maxEventSize = 10 << 20
)

func ForwardGHEventsToLocalApp(ctx context.Context, smeeURL string) {
	targetURL := fmt.Sprintf("http://localhost:%s/webhooks/github", os.Getenv("APP_PORT"))
	client := &http.Client{}

	for ctx.Err() == nil {
		if err := streamOnce(ctx, client, smeeURL, targetURL); err != nil && ctx.Err() == nil {
			log.Printf("proxy: smee stream error: %v", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectDelay):
		}
	}
}

func streamOnce(ctx context.Context, client *http.Client, smeeURL, targetURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, smeeURL, nil)
	if err != nil {
		return fmt.Errorf("build smee request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connect to smee: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("smee returned status %s", resp.Status)
	}
	log.Printf("proxy: connected to %s, forwarding GitHub events to %s", smeeURL, targetURL)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxEventSize)

	var dataLines []string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case line == "" && len(dataLines) > 0:
			payload := strings.Join(dataLines, "\n")
			dataLines = nil
			if err := forwardEvent(ctx, client, targetURL, []byte(payload)); err != nil {
				log.Printf("proxy: failed to forward event: %v", err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read smee stream: %w", err)
	}
	return fmt.Errorf("smee stream closed")
}

func forwardEvent(ctx context.Context, client *http.Client, targetURL string, data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("decode smee event: %w", err)
	}

	body, ok := fields["body"]
	if !ok {
		// Not a webhook delivery
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build forward request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, raw := range fields {
		if !strings.HasPrefix(strings.ToLower(key), "x-") {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			continue
		}
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("forward to local app: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	log.Printf("proxy: forwarded %s event -> %s", req.Header.Get("X-Github-Event"), resp.Status)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("local app responded with status %s", resp.Status)
	}
	return nil
}
