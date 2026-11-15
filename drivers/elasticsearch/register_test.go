package elasticsearch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/paradedb/benchmarker/drivers/opensearch"
)

func TestAPIKeyFromEnvironment(t *testing.T) {
	t.Setenv("ELASTICSEARCH_API_KEY", " encoded-test-key \n")
	if _, err := New("http://localhost:9200"); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("expected API key to require HTTPS, got %v", err)
	}
	d, err := New("https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// OpenSearch shares the HTTP driver but must not read Elasticsearch's key.
	t.Setenv("OPENSEARCH_SKIP_TLS_VERIFY", "")
	os, err := opensearch.New("http://localhost:9201")
	if err != nil {
		t.Fatalf("Elasticsearch key affected OpenSearch: %v", err)
	}
	defer os.Close()
}

func TestWithoutAPIKeyPreservesExistingAuth(t *testing.T) {
	for _, key := range []string{"", " \n\t"} {
		t.Run(fmt.Sprintf("key=%q", key), func(t *testing.T) {
			t.Setenv("ELASTICSEARCH_API_KEY", key)
			for _, basicAuth := range []bool{false, true} {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if basicAuth {
						user, password, ok := r.BasicAuth()
						if !ok || user != "user" || password != "password" {
							t.Error("missing basic auth")
						}
					} else if r.Header.Get("Authorization") != "" {
						t.Error("unexpected authorization header")
					}
					fmt.Fprint(w, `{"hits":{"total":{"value":1}}}`)
				}))
				t.Cleanup(server.Close)
				address := server.URL
				if basicAuth {
					address = strings.Replace(address, "http://", "http://user:password@", 1)
				}
				d, err := New(address)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = d.Close() })
				if n, err := d.Query(context.Background(), `{}`); err != nil || n != 1 {
					t.Fatalf("query: hits=%d err=%v", n, err)
				}
			}
		})
	}
}
