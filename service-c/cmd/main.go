package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-otel-demo/service-c/internal/config"
	"go-otel-demo/service-c/internal/handler"
	redisstore "go-otel-demo/service-c/internal/redis"

	"github.com/gin-gonic/gin"
)

func main() {
	if code := run(); code != 0 {
		os.Exit(code)
	}
}

func run() int {
	configPath := flag.String("config", "config.yaml", "path to YAML config")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Printf("load config failed: %v", err)
		return 1
	}

	// L2 SDK init intentionally removed (otel-auto rollout).
	// OTel bootstrapping happens via OTEL_* env vars + loongsuite-go-agent.

	redisClient, err := redisstore.New(cfg.Redis)
	if err != nil {
		log.Printf("initialize redis client failed: %v", err)
		return 1
	}
	defer func() {
		if err := redisClient.Close(); err != nil {
			log.Printf("close redis client failed: %v", err)
		}
	}()

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery(), handler.TraceIDHeaderMiddleware())
	handler.New(cfg, redisClient).Register(router)

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("%s listening on %s", cfg.Server.Name, server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
			return
		}
		serverErrors <- nil
	}()

	select {
	case sig := <-signals:
		log.Printf("received signal %s, shutting down", sig)
	case err := <-serverErrors:
		if err != nil {
			log.Printf("http server failed: %v", err)
			return 1
		}
		return 0
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("http server graceful shutdown failed: %v", err)
		if closeErr := server.Close(); closeErr != nil {
			log.Printf("force close http server failed: %v", closeErr)
		}
		return 1
	}

	return 0
}
