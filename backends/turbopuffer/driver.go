// Package turbopuffer implements the cloud-only Turbopuffer HTTP backend.
package turbopuffer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/metrics"
)

func init() {
	backends.Register("turbopuffer", backends.BackendConfig{
		Factory: New, FileType: "json", EnvVar: "TURBOPUFFER_URL",
	})
}

var namespacePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
var regionPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// Driver uses native Turbopuffer requests, without retries that hide benchmark errors.
type Driver struct {
	address        string
	apiKey         string
	distanceMetric string
	client         *http.Client
	mu             sync.RWMutex
	writes         map[string]map[string]json.RawMessage
}

// New accepts a regional HTTPS URL, or TURBOPUFFER_URL / TURBOPUFFER_REGION.
// Credentials are read only from TURBOPUFFER_API_KEY, never from the URL.
// The optional distance_metric URL parameter defaults to cosine_distance.
func New(connection string) (backends.Driver, error) {
	key := strings.TrimSpace(os.Getenv("TURBOPUFFER_API_KEY"))
	if key == "" {
		return nil, fmt.Errorf("turbopuffer: TURBOPUFFER_API_KEY is required")
	}
	if connection == "" {
		connection = os.Getenv("TURBOPUFFER_URL")
	}
	if connection == "" {
		region := os.Getenv("TURBOPUFFER_REGION")
		if !regionPattern.MatchString(region) {
			return nil, fmt.Errorf("turbopuffer: set TURBOPUFFER_URL or a valid TURBOPUFFER_REGION")
		}
		connection = "https://" + region + ".turbopuffer.com"
	}
	u, err := url.Parse(connection)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("turbopuffer: connection must be a regional HTTPS URL without credentials or a path")
	}
	params, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("turbopuffer: invalid connection parameters")
	}
	for name := range params {
		if name != "distance_metric" {
			return nil, fmt.Errorf("turbopuffer: unsupported connection parameter %q", name)
		}
	}
	metric := params.Get("distance_metric")
	if metric == "" {
		metric = "cosine_distance"
	}
	if metric != "cosine_distance" && metric != "euclidean_squared" {
		return nil, fmt.Errorf("turbopuffer: invalid distance_metric")
	}
	u.RawQuery = ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	return &Driver{
		address: strings.TrimRight(u.String(), "/"), apiKey: key, distanceMetric: metric,
		client: &http.Client{Timeout: 15 * time.Minute, Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		writes: make(map[string]map[string]json.RawMessage),
	}, nil
}

func (d *Driver) Close() error { d.client.CloseIdleConnections(); return nil }

// NewLoadWorker copies setup options without replaying destructive operations.
func (d *Driver) NewLoadWorker() (backends.Driver, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	writes := make(map[string]map[string]json.RawMessage, len(d.writes))
	for namespace, options := range d.writes {
		writes[namespace] = options
	}
	transport := d.client.Transport.(*http.Transport).Clone()
	client := *d.client
	client.Transport = transport
	return &Driver{address: d.address, apiKey: d.apiKey, distanceMetric: d.distanceMetric, client: &client, writes: writes}, nil
}

func (d *Driver) request(ctx context.Context, method, namespace, endpoint string, body any, ignoreMissing bool, result any) error {
	if !namespacePattern.MatchString(namespace) || namespace == "." || namespace == ".." {
		return fmt.Errorf("turbopuffer: invalid namespace %q", namespace)
	}
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("turbopuffer: encode request: %w", err)
		}
	}
	version := "/v2"
	if endpoint == "/metadata" || endpoint == "/hint_cache_warm" {
		version = "/v1"
	}
	req, err := http.NewRequestWithContext(ctx, method, d.address+version+"/namespaces/"+namespace+endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+d.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("turbopuffer: %w", err)
	}
	defer resp.Body.Close()
	if ignoreMissing && resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return fmt.Errorf("turbopuffer: %s %s%s: HTTP %d: %s", method, namespace, endpoint, resp.StatusCode, strings.ReplaceAll(string(message), d.apiKey, "[REDACTED]"))
	}
	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("turbopuffer: decode response: %w", err)
		}
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// Exec accepts setup {namespace, reset, schema, distance_metric} or an array
// of native operations {namespace, method, endpoint, body}. Setup options are
// attached to subsequent writes because empty namespaces cannot be created.
func (d *Driver) Exec(ctx context.Context, statements string) error {
	raw := bytes.TrimSpace([]byte(statements))
	if len(raw) == 0 {
		return fmt.Errorf("turbopuffer: empty configuration")
	}
	if raw[0] == '[' {
		var entries []json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			return err
		}
		for _, entry := range entries {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(entry, &fields); err != nil || fields == nil {
				return fmt.Errorf("turbopuffer: each operation must be an object")
			}
			if fields["method"] == nil && fields["endpoint"] == nil && fields["body"] == nil && fields["wait_for_index"] == nil {
				if err := d.Exec(ctx, string(entry)); err != nil {
					return err
				}
				continue
			}
			var op struct {
				Namespace    string          `json:"namespace"`
				Method       string          `json:"method"`
				Endpoint     string          `json:"endpoint"`
				Body         json.RawMessage `json:"body"`
				WaitForIndex bool            `json:"wait_for_index"`
				Timeout      string          `json:"timeout"`
			}
			decoder := json.NewDecoder(bytes.NewReader(entry))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&op); err != nil {
				return err
			}
			if op.Namespace == "" {
				op.Namespace = "documents"
			}
			if op.WaitForIndex {
				if op.Method != "" || op.Endpoint != "" || op.Body != nil {
					return fmt.Errorf("turbopuffer: wait_for_index cannot be combined with an HTTP operation")
				}
				timeout := 15 * time.Minute
				if op.Timeout != "" {
					var err error
					timeout, err = time.ParseDuration(op.Timeout)
					if err != nil || timeout <= 0 {
						return fmt.Errorf("turbopuffer: invalid wait_for_index timeout")
					}
				}
				if err := d.waitForIndex(ctx, op.Namespace, timeout); err != nil {
					return err
				}
				continue
			}
			if op.Timeout != "" {
				return fmt.Errorf("turbopuffer: timeout is only supported with wait_for_index")
			}
			if op.Method == "" {
				op.Method = http.MethodPost
			}
			op.Method = strings.ToUpper(op.Method)
			endpoint := strings.TrimPrefix(op.Endpoint, "/")
			switch endpoint {
			case "", "query", "metadata", "hint_cache_warm":
			default:
				return fmt.Errorf("turbopuffer: unsupported endpoint %q", endpoint)
			}
			if endpoint != "" {
				endpoint = "/" + endpoint
			}
			var body any
			if len(op.Body) > 0 {
				body = op.Body
			}
			if err := d.request(ctx, op.Method, op.Namespace, endpoint, body, op.Method == http.MethodDelete && endpoint == "", nil); err != nil {
				return err
			}
		}
		return nil
	}
	var setup struct {
		Namespace           string          `json:"namespace"`
		Reset               bool            `json:"reset"`
		Schema              json.RawMessage `json:"schema"`
		DistanceMetric      string          `json:"distance_metric"`
		DisableBackpressure *bool           `json:"disable_backpressure"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if !json.Valid(raw) {
		return fmt.Errorf("turbopuffer: invalid configuration JSON")
	}
	if err := decoder.Decode(&setup); err != nil {
		return err
	}
	if string(raw) == "null" {
		return fmt.Errorf("turbopuffer: expected configuration object")
	}
	if setup.Namespace == "" {
		setup.Namespace = "documents"
	}
	if !namespacePattern.MatchString(setup.Namespace) || setup.Namespace == "." || setup.Namespace == ".." {
		return fmt.Errorf("turbopuffer: invalid namespace")
	}
	options := make(map[string]json.RawMessage)
	if setup.DisableBackpressure != nil {
		options["disable_backpressure"], _ = json.Marshal(*setup.DisableBackpressure)
	}
	if len(setup.Schema) > 0 {
		var schema map[string]json.RawMessage
		if err := json.Unmarshal(setup.Schema, &schema); err != nil || schema == nil {
			return fmt.Errorf("turbopuffer: schema must be an object")
		}
		options["schema"] = setup.Schema
	}
	if setup.DistanceMetric != "" {
		if setup.DistanceMetric != "cosine_distance" && setup.DistanceMetric != "euclidean_squared" {
			return fmt.Errorf("turbopuffer: invalid distance_metric")
		}
		options["distance_metric"], _ = json.Marshal(setup.DistanceMetric)
	}
	if setup.Reset {
		if err := d.request(ctx, http.MethodDelete, setup.Namespace, "", nil, true, nil); err != nil {
			return err
		}
	}
	d.mu.Lock()
	d.writes[setup.Namespace] = options
	d.mu.Unlock()
	return nil
}

type queryResult struct {
	Rows         []json.RawMessage          `json:"rows"`
	Aggregations map[string]json.RawMessage `json:"aggregations"`
	Groups       []json.RawMessage          `json:"aggregation_groups"`
	Results      []queryResult              `json:"results"`
}

func (r queryResult) hits() int {
	if r.Results != nil {
		total := 0
		for _, result := range r.Results {
			total += result.hits()
		}
		return total
	}
	if r.Rows != nil {
		return len(r.Rows)
	}
	if r.Groups != nil {
		return len(r.Groups)
	}
	keys := make([]string, 0, len(r.Aggregations))
	for key := range r.Aggregations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		var count int
		if string(r.Aggregations[key]) != "null" && json.Unmarshal(r.Aggregations[key], &count) == nil {
			return count
		}
	}
	return 0
}

// Query accepts (namespace, queryObject) or (JSON string, optional namespace).
func (d *Driver) Query(ctx context.Context, query string, args ...any) (int, error) {
	namespace := "documents"
	var body json.RawMessage
	if strings.HasPrefix(strings.TrimSpace(query), "{") {
		if !json.Valid([]byte(query)) {
			return 0, fmt.Errorf("turbopuffer: invalid query JSON")
		}
		body = json.RawMessage(query)
		if len(args) > 0 {
			var ok bool
			namespace, ok = args[0].(string)
			if !ok {
				return 0, fmt.Errorf("turbopuffer: expected namespace string")
			}
		}
	} else {
		namespace = query
		if len(args) == 0 {
			return 0, fmt.Errorf("turbopuffer: missing query object")
		}
		data, err := json.Marshal(args[0])
		if err != nil {
			return 0, err
		}
		body = data
	}
	if trimmed := bytes.TrimSpace(body); len(trimmed) == 0 || trimmed[0] != '{' {
		return 0, fmt.Errorf("turbopuffer: query must be a JSON object")
	}
	var result queryResult
	if err := d.request(ctx, http.MethodPost, namespace, "/query", body, false, &result); err != nil {
		return 0, err
	}
	return result.hits(), nil
}

// Insert upserts complete documents. IDs must be supplied in id (or _id).
func (d *Driver) Insert(ctx context.Context, namespace string, cols []string, rows [][]any) (int, error) {
	key := "id"
	found := false
	for _, col := range cols {
		if col == "id" {
			found = true
		}
	}
	if !found {
		key = "_id"
	} else {
		for _, col := range cols {
			if col == "_id" {
				return 0, fmt.Errorf("turbopuffer: provide id or _id, not both")
			}
		}
	}
	return d.upsert(ctx, namespace, key, cols, rows)
}

// Update replaces entire documents, matching Elasticsearch's upsert semantics.
func (d *Driver) Update(ctx context.Context, namespace string, keyCols, cols []string, rows [][]any) (int, error) {
	if len(keyCols) == 0 {
		return d.Insert(ctx, namespace, cols, rows)
	}
	if len(keyCols) != 1 {
		return 0, fmt.Errorf("turbopuffer: exactly one key column is required")
	}
	return d.upsert(ctx, namespace, keyCols[0], cols, rows)
}

func (d *Driver) upsert(ctx context.Context, namespace, key string, cols []string, rows [][]any) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	keyIndex := -1
	seen := make(map[string]bool)
	for i, col := range cols {
		if seen[col] {
			return 0, fmt.Errorf("turbopuffer: duplicate column %q", col)
		}
		seen[col] = true
		if col == key {
			keyIndex = i
		}
	}
	if keyIndex < 0 {
		return 0, fmt.Errorf("turbopuffer: missing ID column %q", key)
	}
	if key != "id" && seen["id"] {
		return 0, fmt.Errorf("turbopuffer: key column %q conflicts with id", key)
	}
	d.mu.RLock()
	options := d.writes[namespace]
	d.mu.RUnlock()
	vectorFields := map[string]bool{"vector": true}
	var schema map[string]json.RawMessage
	if json.Unmarshal(options["schema"], &schema) == nil {
		for name, raw := range schema {
			var typeName string
			if json.Unmarshal(raw, &typeName) != nil {
				var field struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(raw, &field) == nil {
					typeName = field.Type
				}
			}
			if strings.HasPrefix(typeName, "[") && !strings.HasPrefix(typeName, "[]") {
				vectorFields[name] = true
			}
		}
	}
	docs := make([]map[string]any, len(rows))
	for i, row := range rows {
		if len(row) != len(cols) {
			return 0, fmt.Errorf("turbopuffer: row %d has %d values for %d columns", i, len(row), len(cols))
		}
		if err := validateID(row[keyIndex]); err != nil {
			return 0, fmt.Errorf("turbopuffer: row %d: %w", i, err)
		}
		doc := make(map[string]any, len(cols))
		for j, col := range cols {
			if col == key {
				doc["id"] = row[j]
			} else {
				value := row[j]
				if vector, ok := value.([]float32); ok && vectorFields[col] {
					encoded, err := encodeVector(vector)
					if err != nil {
						return 0, fmt.Errorf("turbopuffer: row %d column %s: %w", i, col, err)
					}
					value = encoded
				}
				doc[col] = value
			}
		}
		docs[i] = doc
	}
	body := map[string]any{"upsert_rows": docs, "distance_metric": d.distanceMetric}
	for name, value := range options {
		body[name] = value
	}
	var result struct {
		RowsAffected *int `json:"rows_affected"`
	}
	if err := d.request(ctx, http.MethodPost, namespace, "", body, false, &result); err != nil {
		return 0, err
	}
	if result.RowsAffected == nil || *result.RowsAffected < 0 || *result.RowsAffected > len(rows) {
		return 0, fmt.Errorf("turbopuffer: write response has missing or invalid rows_affected")
	}
	return *result.RowsAffected, nil
}

// encodeVector uses the API's lossless little-endian float32 wire format.
// Typed Parquet/CSV vectors avoid decimal JSON expansion on bulk imports.
func encodeVector(vector []float32) (string, error) {
	data := make([]byte, 4*len(vector))
	for i, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return "", fmt.Errorf("vector contains a non-finite value")
		}
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(value))
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// CaptureConfig records non-secret connection settings; there is no server
// version endpoint or local container for this managed service.
func (d *Driver) CaptureConfig(_ context.Context, backendName string) {
	metrics.RegisterBackendConfig(backendName, map[string]interface{}{"endpoint": d.address, "distance_metric": d.distanceMetric, "deployment": "cloud"})
}

func validateID(id any) error {
	if id == nil {
		return fmt.Errorf("ID must not be null")
	}
	if number, ok := id.(json.Number); ok {
		if _, err := strconv.ParseUint(string(number), 10, 64); err != nil {
			return fmt.Errorf("ID must be an unsigned 64-bit integer")
		}
		return nil
	}
	value := reflect.ValueOf(id)
	switch value.Kind() {
	case reflect.String:
		if value.Len() > 0 && value.Len() <= 64 {
			return nil
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if value.Int() >= 0 {
			return nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return nil
	case reflect.Float32, reflect.Float64:
		number := value.Float()
		// JS Numbers cannot safely represent all larger integer IDs. Typed CSV and
		// Parquet readers retain int64 values without this floating-point conversion.
		if number >= 0 && number <= 9007199254740991 && math.Trunc(number) == number {
			return nil
		}
	}
	return fmt.Errorf("ID must be a non-empty string of at most 64 bytes or a non-negative integer (use typed data or string IDs above JS's safe integer range)")
}

func (d *Driver) waitForIndex(ctx context.Context, namespace string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		var metadata struct {
			Index struct {
				Status string `json:"status"`
			} `json:"index"`
		}
		if err := d.request(ctx, http.MethodGet, namespace, "/metadata", nil, false, &metadata); err != nil {
			return err
		}
		switch metadata.Index.Status {
		case "up-to-date":
			return nil
		case "updating":
		default:
			return fmt.Errorf("turbopuffer: missing or unknown index status %q", metadata.Index.Status)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("turbopuffer: waiting for index %s: %w", namespace, ctx.Err())
		case <-timer.C:
		}
	}
}
