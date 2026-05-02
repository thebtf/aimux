package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	mb := flag.Int("mb", 50, "megabytes to emit")
	flag.Parse()

	if *mb < 0 {
		fmt.Fprintln(os.Stderr, "mb must be non-negative")
		os.Exit(2)
	}

	const chunkSize = 64 * 1024
	chunk := make([]byte, chunkSize)
	for i := range chunk {
		chunk[i] = 'A'
	}
	chunk[chunkSize-1] = '\n'

	remaining := int64(*mb) * 1024 * 1024
	for remaining > 0 {
		writeSize := int64(len(chunk))
		if remaining < writeSize {
			writeSize = remaining
		}
		if _, err := os.Stdout.Write(chunk[:writeSize]); err != nil {
			fmt.Fprintf(os.Stderr, "write stdout: %v\n", err)
			os.Exit(1)
		}
		remaining -= writeSize
	}
}
