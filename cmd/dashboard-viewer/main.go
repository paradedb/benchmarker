// Command dashboard-viewer serves saved benchmark dashboard JSON files.
//
// Usage:
//
//	dashboard-viewer <file.json>
//	dashboard-viewer <file.json> --export <output.html> [--notes "Description"]
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/paradedb/benchmarks/dashboard"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: dashboard-viewer <dashboard.json> [--export <output.html>] [--notes \"Description\"]")
		fmt.Println("\nViews a saved benchmark dashboard JSON file in your browser.")
		fmt.Println("Use --export to create a single-file HTML viewer with embedded dashboard data.")
		fmt.Println("The exported HTML still loads frontend assets from CDNs.")
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
		arg := os.Args[i]
		switch arg {
		case "--export":
			if i+1 < len(os.Args) {
				exportFile = os.Args[i+1]
				i++
			} else {
				fmt.Println("Error: --export requires a file path")
				os.Exit(1)
			}
		case "--notes":
			if i+1 < len(os.Args) {
				notes = os.Args[i+1]
				i++
			} else {
				fmt.Println("Error: --notes requires a value")
				os.Exit(1)
			}
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Printf("Error: unknown flag: %s\n", arg)
			} else {
				fmt.Printf("Error: unexpected argument: %s\n", arg)
			}
			fmt.Println("Usage: dashboard-viewer <dashboard.json> [--export <output.html>] [--notes \"Description\"]")
			os.Exit(1)
		}
	}

	// Export mode
	if exportFile != "" {
		if err := dashboard.ExportStandalone(filename, exportFile, notes); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Exported HTML viewer to: %s\n", exportFile)
		return
	}

	// View mode (with optional notes)
	if err := dashboard.ServeFile(filename, notes); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
