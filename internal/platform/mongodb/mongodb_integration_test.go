//go:build integration

package mongodb_test

import (
	"context"
	"os"
	"testing"

	"rinotravel-api/internal/platform/mongodb"
)

func TestConnect(t *testing.T) {
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		t.Skip("MONGODB_URI is not set")
	}

	client, err := mongodb.Connect(context.Background(), uri)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := mongodb.Disconnect(client); err != nil {
		t.Errorf("Disconnect() error = %v", err)
	}
}

func TestConnect_FailsFastWhenUnreachable(t *testing.T) {
	_, err := mongodb.Connect(context.Background(), "mongodb://127.0.0.1:1/?serverSelectionTimeoutMS=500")
	if err == nil {
		t.Fatal("Connect() error = nil, want error")
	}
}
