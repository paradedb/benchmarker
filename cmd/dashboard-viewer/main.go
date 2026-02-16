// Command dashboard-viewer serves saved k6-search dashboard JSON files.
//
// Usage:
//
//	dashboard-viewer <file.json>
//	dashboard-viewer <file.json> --export <output.html> [--notes "Description"]
package main

import (
	"fmt"
	"os"

	"github.com/paradedb/benchmarks/dashboard"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: dashboard-viewer <dashboard.json> [--export <output.html>] [--notes \"Description\"]")
		fmt.Println("\nViews a saved k6-search dashboard JSON file in your browser.")
		fmt.Println("Use --export to create a standalone HTML file.")
		fmt.Println("Use --notes to add a description below the title.")
		os.Exit(1)
	}

	filename := os.Args[1]

	// Check file exists
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		fmt.Printf("Error: file not found: %s\n", filename)
		os.Exit(1)
	}

	// Parse flags
	var exportFile, notes string
	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--export":
			if i+1 < len(os.Args) {
				exportFile = os.Args[i+1]
				i++
			}
		case "--notes":
			if i+1 < len(os.Args) {
				notes = os.Args[i+1]
				i++
			}
		}
	}

	// Export mode
	if exportFile != "" {
		if err := dashboard.ExportStandalone(filename, exportFile, notes); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Exported standalone dashboard to: %s\n", exportFile)
		return
	}

	// View mode (with optional notes)
	if err := dashboard.ServeFile(filename, notes); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
