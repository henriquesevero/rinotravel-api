package mongodb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

const (
	serverSelectionTimeout = 5 * time.Second
	pingTimeout            = 5 * time.Second
	disconnectTimeout      = 5 * time.Second
)

func Connect(ctx context.Context, uri string) (*mongo.Client, error) {
	opts := options.Client().ApplyURI(uri).SetServerSelectionTimeout(serverSelectionTimeout)

	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("connect to mongodb: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := client.Ping(pingCtx, readpref.Primary()); err != nil {
		_ = Disconnect(client)
		return nil, fmt.Errorf("ping mongodb: %w", err)
	}
	if err := requireTransactions(pingCtx, client); err != nil {
		_ = Disconnect(client)
		return nil, err
	}
	return client, nil
}

// Atlas is always a replica set. A standalone mongod (the default local setup)
// rejects transactions on the first write, so fail at startup instead.
func requireTransactions(ctx context.Context, client *mongo.Client) error {
	var hello struct {
		SetName string `bson:"setName"`
		Msg     string `bson:"msg"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		return fmt.Errorf("inspect mongodb topology: %w", err)
	}
	if hello.SetName == "" && hello.Msg != "isdbgrid" {
		return errors.New("mongodb must be a replica set or sharded cluster because writes use transactions (see docker-compose.yml)")
	}
	return nil
}

func Disconnect(client *mongo.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), disconnectTimeout)
	defer cancel()
	return client.Disconnect(ctx)
}
