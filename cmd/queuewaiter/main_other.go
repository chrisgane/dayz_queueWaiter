//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "DayZ Queue Waiter runs on Windows only.")
	os.Exit(1)
}
