package vectorchord

import (
	"testing"

	"github.com/paradedb/benchmarker/backends"
)

func TestRegisteredLoaderAcceptsVectorSchema(t *testing.T) {
	config, ok := backends.GetConfig("vectorchord")
	if !ok {
		t.Fatal("VectorChord backend is not registered")
	}
	loader := backends.NewCLILoader("vectorchord", config.FileType, config.DefaultConn, config.Factory)
	if err := loader.ValidateSchema(&backends.Schema{
		Table:   "documents",
		Columns: map[string]string{"id": "bigint", "emb": "vector(3)"},
	}); err != nil {
		t.Fatalf("VectorChord loader rejected vector schema: %v", err)
	}
}
