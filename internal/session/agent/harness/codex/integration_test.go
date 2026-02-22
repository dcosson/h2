package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"h2/internal/session/agent/harness"
	"h2/internal/session/agent/monitor"
)

// TestIntegration_FullPipeline verifies the end-to-end Codex harness flow:
//
//	CodexHarness.PrepareForLaunch → OtelServer (random port)
//	mockCodex sends OTEL traces via HTTP → EventHandler → events → AgentMonitor
//
// Verifies: session ID discovery, token counting, tool tracking, state transitions.
func TestIntegration_FullPipeline(t *testing.T) {
	// 1. Create harness and monitor, wire them together.
	h := New(harness.HarnessConfig{}, nil)
	mon := monitor.New()

	cfg, err := h.PrepareForLaunch("test-codex", "", false)
	if err != nil {
		t.Fatalf("PrepareForLaunch: %v", err)
	}
	defer h.Stop()

	// Verify we got -c flag with OTEL endpoint.
	if len(cfg.PrependArgs) != 2 || cfg.PrependArgs[0] != "-c" {
		t.Fatalf("PrependArgs = %v, want [-c, ...]", cfg.PrependArgs)
	}

	port := h.OtelPort()
	if port == 0 {
		t.Fatal("OtelPort should be non-zero after PrepareForLaunch")
	}

	// 2. Start harness and monitor.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	externalEvents := make(chan monitor.AgentEvent, 256)
	go h.Start(ctx, externalEvents)
	go mon.Run(ctx)

	// Bridge: forward external events to the monitor.
	go func() {
		for {
			select {
			case ev := <-externalEvents:
				mon.Events() <- ev
			case <-ctx.Done():
				return
			}
		}
	}()

	// Give goroutines time to start.
	time.Sleep(20 * time.Millisecond)

	// 3. Simulate Codex sending OTEL log events.
	logsURL := fmt.Sprintf("http://127.0.0.1:%d/v1/logs", port)

	// 3a. conversation_starts → session ID discovery
	postLog(t, logsURL, "codex.conversation_starts", []otelAttribute{
		{Key: "conversation.id", Value: otelAttrValue{StringValue: "conv-integration-1"}},
		{Key: "model", Value: otelAttrValue{StringValue: "o3-mini"}},
	})

	// 3b. user_prompt → turn started
	postLog(t, logsURL, "codex.user_prompt", []otelAttribute{
		{Key: "prompt_length", Value: otelAttrValue{IntValue: json.RawMessage("25")}},
	})

	// 3c. tool_result → tool tracking
	postLog(t, logsURL, "codex.tool_result", []otelAttribute{
		{Key: "tool_name", Value: otelAttrValue{StringValue: "shell"}},
		{Key: "call_id", Value: otelAttrValue{StringValue: "call-1"}},
		{Key: "duration_ms", Value: otelAttrValue{IntValue: json.RawMessage("250")}},
		{Key: "success", Value: otelAttrValue{StringValue: "true"}},
	})

	// 3d. sse_event (response.completed) → token counting
	postLog(t, logsURL, "codex.sse_event", []otelAttribute{
		{Key: "event.kind", Value: otelAttrValue{StringValue: "response.completed"}},
		{Key: "input_token_count", Value: otelAttrValue{IntValue: json.RawMessage("1000")}},
		{Key: "output_token_count", Value: otelAttrValue{IntValue: json.RawMessage("500")}},
		{Key: "cached_token_count", Value: otelAttrValue{IntValue: json.RawMessage("200")}},
	})

	// 3e. tool_decision (ask_user) → approval requested
	postLog(t, logsURL, "codex.tool_decision", []otelAttribute{
		{Key: "tool_name", Value: otelAttrValue{StringValue: "shell"}},
		{Key: "call_id", Value: otelAttrValue{StringValue: "call-2"}},
		{Key: "decision", Value: otelAttrValue{StringValue: "ask_user"}},
	})

	// 3f. Second tool_result → accumulation
	postLog(t, logsURL, "codex.tool_result", []otelAttribute{
		{Key: "tool_name", Value: otelAttrValue{StringValue: "shell"}},
		{Key: "call_id", Value: otelAttrValue{StringValue: "call-2"}},
		{Key: "duration_ms", Value: otelAttrValue{IntValue: json.RawMessage("100")}},
		{Key: "success", Value: otelAttrValue{StringValue: "true"}},
	})

	// 4. Wait for events to propagate through the pipeline.
	waitFor(t, 2*time.Second, "session ID", func() bool {
		return mon.ThreadID() == "conv-integration-1"
	})

	waitFor(t, 2*time.Second, "model", func() bool {
		return mon.Model() == "o3-mini"
	})

	waitFor(t, 2*time.Second, "token counts", func() bool {
		m := mon.Metrics()
		return m.InputTokens == 1000 && m.OutputTokens == 500 && m.CachedTokens == 200
	})

	waitFor(t, 2*time.Second, "turn count", func() bool {
		return mon.Metrics().TurnCount == 1
	})

	waitFor(t, 2*time.Second, "tool counts", func() bool {
		m := mon.Metrics()
		return m.ToolCounts["shell"] == 2
	})

	// 5. Verify HandleHookEvent returns false (Codex doesn't use hooks).
	if h.HandleHookEvent("PreToolUse", nil) {
		t.Error("HandleHookEvent should return false for Codex")
	}
}

// TestIntegration_MultipleLogPayloads verifies that the harness handles
// multiple log records in a single /v1/logs POST (batch delivery).
func TestIntegration_MultipleLogPayloads(t *testing.T) {
	h := New(harness.HarnessConfig{}, nil)
	mon := monitor.New()

	_, err := h.PrepareForLaunch("test-codex", "", false)
	if err != nil {
		t.Fatalf("PrepareForLaunch: %v", err)
	}
	defer h.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	externalEvents := make(chan monitor.AgentEvent, 256)
	go h.Start(ctx, externalEvents)
	go mon.Run(ctx)
	go func() {
		for {
			select {
			case ev := <-externalEvents:
				mon.Events() <- ev
			case <-ctx.Done():
				return
			}
		}
	}()
	time.Sleep(20 * time.Millisecond)

	// Send multiple log records in a single POST (batch).
	logsURL := fmt.Sprintf("http://127.0.0.1:%d/v1/logs", h.OtelPort())
	payload := otelLogsPayload{
		ResourceLogs: []otelResourceLogs{{
			ScopeLogs: []otelScopeLogs{{
				LogRecords: []otelLogRecord{
					{
						Attributes: []otelAttribute{
							{Key: "event.name", Value: otelAttrValue{StringValue: "codex.conversation_starts"}},
							{Key: "conversation.id", Value: otelAttrValue{StringValue: "batch-conv"}},
							{Key: "model", Value: otelAttrValue{StringValue: "o3"}},
						},
					},
					{
						Attributes: []otelAttribute{
							{Key: "event.name", Value: otelAttrValue{StringValue: "codex.user_prompt"}},
							{Key: "prompt_length", Value: otelAttrValue{IntValue: json.RawMessage("10")}},
						},
					},
					{
						Attributes: []otelAttribute{
							{Key: "event.name", Value: otelAttrValue{StringValue: "codex.sse_event"}},
							{Key: "event.kind", Value: otelAttrValue{StringValue: "response.completed"}},
							{Key: "input_token_count", Value: otelAttrValue{IntValue: json.RawMessage("800")}},
							{Key: "output_token_count", Value: otelAttrValue{IntValue: json.RawMessage("400")}},
							{Key: "cached_token_count", Value: otelAttrValue{IntValue: json.RawMessage("0")}},
						},
					},
				},
			}},
		}},
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(logsURL, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST /v1/logs: %v", err)
	}
	resp.Body.Close()

	waitFor(t, 2*time.Second, "batch session ID", func() bool {
		return mon.ThreadID() == "batch-conv"
	})
	waitFor(t, 2*time.Second, "batch tokens", func() bool {
		m := mon.Metrics()
		return m.InputTokens == 800 && m.OutputTokens == 400
	})
	waitFor(t, 2*time.Second, "batch turn count", func() bool {
		return mon.Metrics().TurnCount == 1
	})
}

// --- Test helpers ---

// postLog sends a single-record OTEL logs payload to the given URL.
func postLog(t *testing.T, url, eventName string, attrs []otelAttribute) {
	t.Helper()
	body := makeLogsPayload(eventName, attrs)
	resp, err := http.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST %s: %v", eventName, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: status %d", eventName, resp.StatusCode)
	}
}

// waitFor polls a condition with a deadline, failing the test if not met.
func waitFor(t *testing.T, timeout time.Duration, desc string, condition func() bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if condition() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", desc)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
