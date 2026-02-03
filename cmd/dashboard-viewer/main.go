// Command dashboard-viewer serves saved k6-search dashboard JSON files.
//
// Usage:
//
//	dashboard-viewer <file.json>
package main

import (
	"fmt"
	"os"

	"github.com/jamesblackwood-sewell/xk6-search/dashboard"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: dashboard-viewer <dashboard.json>")
		fmt.Println("\nViews a saved k6-search dashboard JSON file in your browser.")
		os.Exit(1)
	}

	filename := os.Args[1]

	// Check file exists
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		fmt.Printf("Error: file not found: %s\n", filename)
		os.Exit(1)
	}

	if err := dashboard.ServeFile(filename); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
