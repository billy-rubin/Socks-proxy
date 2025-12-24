package main

import (
	"flag"
	"os"
	"socks-proxy/internal/application"
	"socks-proxy/internal/infrastructure/epoll"
	"socks-proxy/pkg/logger"
)

func main() {
	port := flag.Int("port", 1080, "Port to listen on")
	flag.Parse()

	log := logger.Setup()
	log.Info("Initializing SOCKS5 Proxy...")

	eventLoop, err := epoll.New()
	if err != nil {
		log.Error("Failed to create event loop", "error", err)
		os.Exit(1)
	}

	proxy, err := application.NewProxyService(eventLoop, log, *port)
	if err != nil {
		log.Error("Failed to create proxy service", "error", err)
		os.Exit(1)
	}

	log.Info("Proxy listening", "port", *port)

	if err := proxy.Start(); err != nil {
		log.Error("Proxy stopped unexpectedly", "error", err)
	}
}
