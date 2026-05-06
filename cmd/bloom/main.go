package main

import (
	"os"

	"github.com/stellarjmr/bloom/internal/bloom"
)

func main() {
	os.Exit(bloom.Main(os.Args[1:], os.Stdout, os.Stderr))
}
