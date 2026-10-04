package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func check(url string) error {
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(url)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy status %d", response.StatusCode)
	}
	return nil
}

func main() {
	if len(os.Args) != 2 {
		os.Exit(1)
	}
	if err := check(os.Args[1]); err != nil {
		os.Exit(1)
	}
}
