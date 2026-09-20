//go:build integration

package mongotest

import (
	"context"
	"os"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/platform/mongodb"
)

func Database(t *testing.T) *mongo.Database {
	t.Helper()

	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		t.Skip("MONGODB_URI is not set")
	}

	client, err := mongodb.Connect(context.Background(), uri)
	if err != nil {
		t.Fatalf("connect to mongodb: %v", err)
	}

	db := client.Database("rinotravel_test_" + ids.New()[24:])
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = mongodb.Disconnect(client)
	})
	return db
}
