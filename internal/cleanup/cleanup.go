// Package cleanup reports failures during best-effort resource cleanup.
package cleanup

import (
	"io"
	"log"
)

// Close logs cleanup failures without replacing the operation's primary error.
func Close(closer io.Closer) {
	if err := closer.Close(); err != nil {
		log.Printf("close %T: %v", closer, err)
	}
}
