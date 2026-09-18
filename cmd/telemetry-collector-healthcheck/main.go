package main

import (
	"context"
	"os"
	"time"

	"github.com/Stealth-deplover/stealth/internal/collectorhealthcheck"
)

func main() {
	endpoint := "http://127.0.0.1:13133/"
	if len(os.Args) > 1 && os.Args[1] != "" {
		endpoint = os.Args[1]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := collectorhealthcheck.Check(ctx, endpoint); err != nil {
		os.Exit(1)
	}
}
