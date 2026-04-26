package main

import (
	"fmt"
	"os"
	"time"

	"telegramtrainapp/internal/config"
	"telegramtrainapp/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	link, err := web.MintTestLoginURL(cfg, time.Now().UTC())
	if err != nil {
		fmt.Fprintf(os.Stderr, "mint test login link: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(link)
}
