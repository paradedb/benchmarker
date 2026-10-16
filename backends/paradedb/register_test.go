package paradedb

import (
	"github.com/paradedb/benchmarker/backends"
	"reflect"
	"testing"
)

func TestLegacyImportRegistersOnlyParadeDB(t *testing.T) {
	if got := backends.RegisteredBackends(); !reflect.DeepEqual(got, []string{"paradedb"}) {
		t.Fatalf("import side effects changed: registered %v", got)
	}
}
