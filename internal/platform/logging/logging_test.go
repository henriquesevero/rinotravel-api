package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"rinotravel-api/internal/platform/logging"
	"rinotravel-api/internal/platform/requestid"
)

func TestNew_JSONIncludesRequestIDFromContext(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(&buf, slog.LevelInfo, true)

	ctx := requestid.WithContext(context.Background(), "req-1")
	logger.With("component", "test").InfoContext(ctx, "hello")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log output is not JSON: %v", err)
	}
	if entry["request_id"] != "req-1" || entry["msg"] != "hello" || entry["component"] != "test" {
		t.Errorf("unexpected log entry: %v", entry)
	}
}

func TestNew_OmitsRequestIDWithoutContextValue(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(&buf, slog.LevelInfo, true)

	logger.Info("hello")

	if strings.Contains(buf.String(), "request_id") {
		t.Errorf("log entry unexpectedly has request_id: %s", buf.String())
	}
}

func TestNew_RespectsLevelAndTextFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(&buf, slog.LevelWarn, false)

	logger.Info("ignored")
	logger.Warn("kept")

	out := buf.String()
	if strings.Contains(out, "ignored") || !strings.Contains(out, "msg=kept") {
		t.Errorf("unexpected log output: %q", out)
	}
}
