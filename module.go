// Package search provides a k6 extension for benchmarking search backends.
package search

import (
	_ "github.com/jamesblackwood-sewell/xk6-search/dashboard"
	"github.com/jamesblackwood-sewell/xk6-search/loader"
	"github.com/jamesblackwood-sewell/xk6-search/metrics"
	"go.k6.io/k6/js/modules"
)

func init() {
	modules.Register("k6/x/search", new(RootModule))
}

// RootModule is the global module instance that will create client instances for each VU.
type RootModule struct{}

// NewModuleInstance creates a new instance of the module for each VU.
func (*RootModule) NewModuleInstance(vu modules.VU) modules.Instance {
	return &ModuleInstance{
		vu: vu,
	}
}

// ModuleInstance represents an instance of the module for a single VU.
type ModuleInstance struct {
	vu modules.VU
}

// Exports returns the exports of the module.
func (m *ModuleInstance) Exports() modules.Exports {
	return modules.Exports{
		Named: map[string]interface{}{
			"backends": m.newBackends,
			"metrics":  m.newMetrics,
			"loader":   m.newLoader,
		},
	}
}

// newMetrics creates a new metrics collector.
func (m *ModuleInstance) newMetrics(config map[string]interface{}) *metrics.Collector {
	return metrics.NewCollector(m.vu, config)
}

// newLoader creates a new data loader.
func (m *ModuleInstance) newLoader() *loader.Loader {
	return loader.NewLoader(m.vu)
}
