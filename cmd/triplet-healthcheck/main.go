package main

import (
	"flag"
	"net/http"
	"os"
	"time"
)

func main() {
	url := flag.String("url", "http://127.0.0.1:8080/healthz", "healthcheck URL")
	timeout := flag.Duration("timeout", 2*time.Second, "request timeout")
	flag.Parse()

	client := http.Client{Timeout: *timeout}
	resp, err := client.Get(*url)
	if err != nil {
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		os.Exit(1)
	}
}
