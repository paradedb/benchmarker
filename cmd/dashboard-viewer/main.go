// Command dashboard-viewer serves saved k6-search dashboard JSON files.
//
// Usage:
//
//	dashboard-viewer <file.json>
//	dashboard-viewer <file.json> --export <output.html>
package main

import (
	"fmt"
	"os"

	"github.com/paradedb/benchmarks/dashboard"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: dashboard-viewer <dashboard.json> [--export <output.html>]")
		fmt.Println("\nViews a saved k6-search dashboard JSON file in your browser.")
		fmt.Println("Use --export to create a standalone HTML file.")
		os.Exit(1)
	}

	filename := os.Args[1]

	// Check file exists
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		fmt.Printf("Error: file not found: %s\n", filename)
		os.Exit(1)
	}

	// Check for --export flag
	if len(os.Args) >= 4 && os.Args[2] == "--export" {
		outputFile := os.Args[3]
		if err := dashboard.ExportStandalone(filename, outputFile); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Exported standalone dashboard to: %s\n", outputFile)
		return
	}

	if err := dashboard.ServeFile(filename); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
