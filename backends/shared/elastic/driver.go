// Package elastic provides the shared Elasticsearch/OpenSearch driver implementation.
// Individual backends (elasticsearch, opensearch) import this package and register themselves.
package elastic

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/paradedb/benchmarks/backends"
	"github.com/paradedb/benchmarks/metrics"
)

// DriverConfig holds backend-specific configuration.
type DriverConfig struct {
	SkipTLSVerify    bool   // Allows self-signed HTTPS endpoints in development.
	VersionInfoField string // "build_flavor" for ES, "distribution" for OS
}

// Driver implements the backends.Driver interface for Elasticsearch/OpenSearch.
type Driver struct {
	address string
	client  *http.Client
	config  DriverConfig
}

// New creates a new Elasticsearch/OpenSearch driver.
func New(address string, config DriverConfig) (backends.Driver, error) {
	transport := &http.Transport{}
	if config.SkipTLSVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Driver{
		address: strings.TrimSuffix(address, "/"),
		client:  &http.Client{Timeout: 15 * time.Minute, Transport: transport},
		config:  config,
	}, nil
}

// Close is a no-op.
func (d *Driver) Close() error { return nil }

// Exec executes JSON configuration (create index, update settings, etc).
func (d *Driver) Exec(ctx context.Context, statements string) error {
	// Try parsing as array first (post.json format)
	var operations []map[string]interface{}
	if err := json.Unmarshal([]byte(statements), &operations); err == nil {
		return d.execOperations(ctx, operations)
	}

	// Parse as single object (pre.json format)
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(statements), &config); err != nil {
		return err
	}

	index := "documents"
	if idx, ok := config["index"].(string); ok {
		index = idx
	}

	return d.createIndex(ctx, index, config)
}

func (d *Driver) createIndex(ctx context.Context, index string, config map[string]interface{}) error {
	// Delete existing index
	req, _ := http.NewRequestWithContext(ctx, "DELETE", fmt.Sprintf("%s/%s", d.address, index), nil)
	resp, err := d.client.Do(req)
	if err == nil && resp != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotFound {
			return fmt.Errorf("delete index failed with status %d", resp.StatusCode)
		}
	}

	// Remove "index" key before sending - it's only used for routing
	delete(config, "index")

	// Create with settings
	body, _ := json.Marshal(config)
	req, _ = http.NewRequestWithContext(ctx, "PUT", fmt.Sprintf("%s/%s", d.address, index), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err = d.client.Do(req)
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

func (d *Driver) execOperations(ctx context.Context, operations []map[string]interface{}) error {
	index := "documents"

	for _, op := range operations {
		// Get index override if specified
		if idx, ok := op["index"].(string); ok {
			index = idx
		}

		endpoint, ok := op["endpoint"].(string)
		if !ok && op["method"] == nil {
			continue
		}

		// Build URL with query params
		opURL := fmt.Sprintf("%s/%s", d.address, index)
		if endpoint != "" {
			opURL += "/" + strings.TrimPrefix(endpoint, "/")
		}
		if params, ok := op["params"].(map[string]interface{}); ok {
			query := url.Values{}
			for k, v := range params {
				query.Set(k, fmt.Sprintf("%v", v))
			}
			if encoded := query.Encode(); encoded != "" {
				opURL += "?" + encoded
			}
		}

		// Determine method and body
		method := "POST"
		hasExplicitMethod := false
		if m, ok := op["method"].(string); ok && m != "" {
			method = strings.ToUpper(m)
			hasExplicitMethod = true
		}
		var bodyReader io.Reader
		if body, ok := op["body"]; ok {
			if !hasExplicitMethod {
				method = "PUT"
			}
			bodyBytes, err := json.Marshal(body)
			if err != nil {
				return fmt.Errorf("failed to marshal operation body: %w", err)
			}
			bodyReader = bytes.NewReader(bodyBytes)
		}

		req, _ := http.NewRequestWithContext(ctx, method, opURL, bodyReader)
		if bodyReader != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := d.client.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode >= 400 {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return fmt.Errorf("operation %s %s failed: %s", method, opURL, string(respBody))
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	return nil
}

type aggregationResult struct {
	Buckets []interface{} `json:"buckets"`
	Value   *float64      `json:"value"` // for cardinality/single-value aggs
}

func aggregationHitCount(aggregations map[string]aggregationResult) (int, bool) {
	if len(aggregations) == 0 {
		return 0, false
	}

	keys := make([]string, 0, len(aggregations))
	for key := range aggregations {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	// Prefer bucket aggregations because they represent grouped result sets.
	for _, key := range keys {
		if count := len(aggregations[key].Buckets); count > 0 {
			return count, true
		}
	}

	for _, key := range keys {
		if value := aggregations[key].Value; value != nil {
			return int(*value), true
		}
	}

	return 0, false
}

// Query executes a search query and returns the hit count.
// Supports two call patterns:
//   - Query(ctx, jsonQueryString) - query is a JSON string
//   - Query(ctx, indexName, queryObject) - index name + query map (from JS)
func (d *Driver) Query(ctx context.Context, query string, args ...any) (int, error) {
	var jsonBody []byte
	index := "documents"

	// Try to parse query as JSON first (avoid unmarshal/re-marshal cycle)
	if json.Valid([]byte(query)) {
		// Valid JSON - use directly
		jsonBody = []byte(query)
		// Check if index provided in args
		if len(args) > 0 {
			if idx, ok := args[0].(string); ok {
				index = idx
			}
		}
	} else {
		// Not valid JSON - treat query as index name, args[0] as query object
		index = query
		if len(args) > 0 {
			if queryMap, ok := args[0].(map[string]interface{}); ok {
				var err error
				jsonBody, err = json.Marshal(queryMap)
				if err != nil {
					return 0, fmt.Errorf("failed to marshal query: %w", err)
				}
			} else {
				return 0, fmt.Errorf("expected query object as second argument")
			}
		} else {
			return 0, fmt.Errorf("missing query object")
		}
	}

	req, _ := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/%s/_search?request_cache=false", d.address, index), bytes.NewReader(jsonBody))
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
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []interface{} `json:"hits"`
		} `json:"hits"`
		Aggregations map[string]aggregationResult `json:"aggregations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	// Return hits array length if documents returned
	if len(result.Hits.Hits) > 0 {
		return len(result.Hits.Hits), nil
	}

	// For aggregations, return a deterministic bucket count or single value.
	if count, ok := aggregationHitCount(result.Aggregations); ok {
		return count, nil
	}

	// Fall back to total hits (for count queries with size:0)
	return result.Hits.Total.Value, nil
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
		Errors bool `json:"errors"`
		Items  []struct {
			Index struct {
				Error struct {
					Type   string `json:"type"`
					Reason string `json:"reason"`
				} `json:"error"`
			} `json:"index"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("failed to decode bulk response: %w", err)
	}

	if result.Errors {
		// Find first error
		for _, item := range result.Items {
			if item.Index.Error.Type != "" {
				return 0, fmt.Errorf("bulk insert error: %s - %s", item.Index.Error.Type, item.Index.Error.Reason)
			}
		}
	}

	return len(result.Items), nil
}

// CaptureConfig captures cluster configuration and registers it with metrics.
func (d *Driver) CaptureConfig(ctx context.Context, backendName string) {
	config := make(map[string]interface{})

	req, _ := http.NewRequestWithContext(ctx, "GET", d.address, nil)
	resp, err := d.client.Do(req)
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
				if d.config.VersionInfoField != "" {
					config[d.config.VersionInfoField] = version[d.config.VersionInfoField]
				}
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
