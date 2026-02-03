// Package mongodb provides the MongoDB driver implementation.
package mongodb

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jamesblackwood-sewell/xk6-search/backends"
	"github.com/jamesblackwood-sewell/xk6-search/metrics"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func init() {
	backends.Register("mongodb", backends.BackendConfig{
		Factory:     New,
		FileType:    "json",
		EnvVar:      "MONGODB_URL",
		DefaultConn: "mongodb://localhost:27017",
		Container:   "mongodb",
	})
}

// Driver implements the backends.Driver interface for MongoDB.
type Driver struct {
	client   *mongo.Client
	database string
}

// New creates a new MongoDB driver.
func New(connString string) (backends.Driver, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Add directConnection for local development
	if !strings.Contains(connString, "directConnection") {
		if strings.Contains(connString, "?") {
			connString += "&directConnection=true"
		} else {
			if !strings.HasSuffix(connString, "/") {
				connString += "/"
			}
			connString += "?directConnection=true"
		}
	}

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(connString).SetMaxPoolSize(20).SetMinPoolSize(5))
	if err != nil {
		return nil, err
	}

	if err := client.Ping(ctx, nil); err != nil {
		client.Disconnect(ctx)
		return nil, err
	}

	return &Driver{client: client, database: "benchmark"}, nil
}

// Close disconnects the client.
func (d *Driver) Close() error {
	if d.client != nil {
		return d.client.Disconnect(context.Background())
	}
	return nil
}

// Exec executes JSON configuration (drop collection, create search index, etc).
func (d *Driver) Exec(ctx context.Context, statements string) error {
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(statements), &config); err != nil {
		return err
	}

	database := d.database
	if db, ok := config["database"].(string); ok {
		database = db
	}

	collection := "documents"
	if coll, ok := config["collection"].(string); ok {
		collection = coll
	}

	coll := d.client.Database(database).Collection(collection)

	// Handle drop
	if drop, ok := config["drop"].(bool); ok && drop {
		coll.Drop(ctx)
	}

	// Handle search index creation
	if searchIndex, ok := config["searchIndex"].(map[string]interface{}); ok {
		indexName := "content_search"
		if name, ok := searchIndex["name"].(string); ok {
			indexName = name
		}

		definition := searchIndex["definition"]
		if definition == nil {
			definition = searchIndex
		}

		model := mongo.SearchIndexModel{
			Definition: definition,
			Options:    options.SearchIndexes().SetName(indexName),
		}

		_, err := coll.SearchIndexes().CreateOne(ctx, model)
		if err != nil && !strings.Contains(err.Error(), "already exists") {
			return err
		}

		// Wait for index to be ready
		d.waitForSearchIndex(ctx, coll, indexName, 120*time.Second)
	}

	return nil
}

func (d *Driver) waitForSearchIndex(ctx context.Context, coll *mongo.Collection, indexName string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cursor, err := coll.SearchIndexes().List(ctx, options.SearchIndexes().SetName(indexName))
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		var indexes []bson.M
		cursor.All(ctx, &indexes)

		for _, idx := range indexes {
			if idx["name"] == indexName && idx["status"] == "READY" {
				return
			}
		}
		time.Sleep(2 * time.Second)
	}
}

// Query executes a search aggregation and returns the hit count.
func (d *Driver) Query(ctx context.Context, query string, args ...any) (int, error) {
	var searchStage map[string]interface{}
	if err := json.Unmarshal([]byte(query), &searchStage); err != nil {
		return 0, err
	}

	collection := "documents"
	if len(args) > 0 {
		if coll, ok := args[0].(string); ok {
			collection = coll
		}
	}

	pipeline := mongo.Pipeline{
		{{Key: "$search", Value: searchStage}},
	}

	cursor, err := d.client.Database(d.database).Collection(collection).Aggregate(ctx, pipeline)
	if err != nil {
		return 0, err
	}
	defer cursor.Close(ctx)

	count := 0
	for cursor.Next(ctx) {
		count++
	}
	return count, cursor.Err()
}

// Insert bulk inserts documents.
func (d *Driver) Insert(ctx context.Context, collection string, cols []string, rows [][]any) (int, error) {
	docs := make([]interface{}, len(rows))
	for i, row := range rows {
		doc := bson.M{}
		for j, col := range cols {
			if col == "id" {
				doc["_id"] = row[j]
			} else {
				doc[col] = row[j]
			}
		}
		docs[i] = doc
	}

	result, err := d.client.Database(d.database).Collection(collection).InsertMany(ctx, docs)
	if err != nil {
		return 0, err
	}

	return len(result.InsertedIDs), nil
}

// CaptureConfig captures database configuration and registers it with metrics.
func (d *Driver) CaptureConfig(ctx context.Context, backendName string) {
	config := make(map[string]interface{})

	var result bson.M
	if d.client.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&result) == nil {
		if version, ok := result["version"]; ok {
			config["version"] = version
		}
	}

	metrics.RegisterBackendConfig(backendName, config)
}
