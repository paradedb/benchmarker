// Package elasticsearch provides the Elasticsearch driver implementation.
package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/metrics"
)

func init() {
	backends.Register("elasticsearch", backends.BackendConfig{
		Factory:     New,
		FileType:    "json",
		EnvVar:      "ELASTICSEARCH_URL",
		DefaultConn: "http://localhost:9200",
		Container:   "elasticsearch",
	})
}

// Driver implements the backends.Driver interface for Elasticsearch.
type Driver struct {
	address      string
	client       *http.Client
	requestCache bool // whether to use request cache (default false)
}

// New creates a new Elasticsearch driver.
// Supports URL query params: ?request_cache=true
func New(address string) (backends.Driver, error) {
	requestCache := false

	// Parse URL to extract settings
	if u, err := url.Parse(address); err == nil {
		if u.Query().Get("request_cache") == "true" {
			requestCache = true
		}
		// Remove query params from address
		u.RawQuery = ""
		address = u.String()
	}

	return &Driver{
		address:      strings.TrimSuffix(address, "/"),
		client:       &http.Client{Timeout: 5 * time.Minute},
		requestCache: requestCache,
	}, nil
}

// Close is a no-op for Elasticsearch.
func (d *Driver) Close() error { return nil }

// Exec executes JSON configuration (create index, update settings, etc).
func (d *Driver) Exec(ctx context.Context, statements string) error {
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(statements), &config); err != nil {
		return err
	}

	index := "documents"
	if idx, ok := config["index"].(string); ok {
		index = idx
	}

	// Check if this is pre (has mappings/settings) or post (has refresh/forcemerge)
	if _, hasMappings := config["mappings"]; hasMappings {
		return d.createIndex(ctx, index, config)
	}
	if _, hasSettings := config["settings"]; hasSettings && config["mappings"] == nil {
		return d.postLoad(ctx, index, config)
	}

	// Default: try to create index
	return d.createIndex(ctx, index, config)
}

func (d *Driver) createIndex(ctx context.Context, index string, config map[string]interface{}) error {
	// Delete existing index
	req, _ := http.NewRequestWithContext(ctx, "DELETE", fmt.Sprintf("%s/%s", d.address, index), nil)
	d.client.Do(req)

	// Remove "index" key before sending - it's only used for routing
	delete(config, "index")

	// Create with settings
	body, _ := json.Marshal(config)
	req, _ = http.NewRequestWithContext(ctx, "PUT", fmt.Sprintf("%s/%s", d.address, index), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create index failed: %s", string(body))
	}
	return nil
}

func (d *Driver) postLoad(ctx context.Context, index string, config map[string]interface{}) error {
	// Refresh
	req, _ := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/%s/_refresh", d.address, index), nil)
	d.client.Do(req)

	// Force merge if requested
	if segments, ok := config["max_num_segments"].(float64); ok {
		req, _ = http.NewRequestWithContext(ctx, "POST",
			fmt.Sprintf("%s/%s/_forcemerge?max_num_segments=%d", d.address, index, int(segments)), nil)
		d.client.Do(req)
	}
	return nil
}

// Query executes a search query and returns the hit count.
// Supports two call patterns:
//   - Query(ctx, jsonQueryString) - query is a JSON string
//   - Query(ctx, indexName, queryObject) - index name + query map (from JS)
func (d *Driver) Query(ctx context.Context, query string, args ...any) (int, error) {
	var body map[string]interface{}
	index := "documents"

	// Try to parse query as JSON first
	if err := json.Unmarshal([]byte(query), &body); err != nil {
		// Not valid JSON - treat query as index name, args[0] as query object
		index = query
		if len(args) > 0 {
			if queryMap, ok := args[0].(map[string]interface{}); ok {
				body = queryMap
			} else {
				return 0, fmt.Errorf("expected query object as second argument")
			}
		} else {
			return 0, fmt.Errorf("missing query object")
		}
	} else {
		// Valid JSON - check if index provided in args
		if len(args) > 0 {
			if idx, ok := args[0].(string); ok {
				index = idx
			}
		}
	}

	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/%s/_search?request_cache=%t", d.address, index, d.requestCache), bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("search failed: %s", string(body))
	}

	var result struct {
		Hits struct {
			Hits []interface{} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	return len(result.Hits.Hits), nil
}

// Insert bulk inserts documents.
func (d *Driver) Insert(ctx context.Context, index string, cols []string, rows [][]any) (int, error) {
	var body strings.Builder

	for _, row := range rows {
		doc := make(map[string]interface{})
		for i, col := range cols {
			doc[col] = row[i]
		}

		// Action line
		body.WriteString(fmt.Sprintf(`{"index":{"_index":"%s"}}`, index))
		body.WriteByte('\n')

		// Document line
		docJSON, _ := json.Marshal(doc)
		body.Write(docJSON)
		body.WriteByte('\n')
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/_bulk", d.address), strings.NewReader(body.String()))
	req.Header.Set("Content-Type", "application/x-ndjson")

	resp, err := d.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("bulk insert failed: %s", string(respBody))
	}

	var result struct {
		Items []interface{} `json:"items"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	return len(result.Items), nil
}

// CaptureConfig captures cluster configuration and registers it with metrics.
func (d *Driver) CaptureConfig(ctx context.Context, backendName string) {
	config := make(map[string]interface{})

	resp, err := d.client.Get(d.address)
	if err != nil {
		fmt.Printf("[%s] config capture failed: %v\n", backendName, err)
	} else if resp.StatusCode == 200 {
		defer resp.Body.Close()
		var info map[string]interface{}
		if json.NewDecoder(resp.Body).Decode(&info) == nil {
			if clusterName, ok := info["cluster_name"].(string); ok {
				config["cluster_name"] = clusterName
			}
			if version, ok := info["version"].(map[string]interface{}); ok {
				config["version"] = version["number"]
				config["build_flavor"] = version["build_flavor"]
				config["build_type"] = version["build_type"]
				config["lucene_version"] = version["lucene_version"]
			}
		}
	} else {
		resp.Body.Close()
		fmt.Printf("[%s] config capture failed: HTTP %d\n", backendName, resp.StatusCode)
	}

	metrics.RegisterBackendConfig(backendName, config)
}
