package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/windowsfreak/leben/internal/auth"
	"github.com/windowsfreak/leben/internal/config"
	"github.com/windowsfreak/leben/internal/db"
	"github.com/windowsfreak/leben/internal/mcp"
	"github.com/windowsfreak/leben/internal/router"
	"github.com/windowsfreak/leben/internal/services"
	"github.com/windowsfreak/leben/internal/tasks"
)

func main() {
	log.Println("Starting Leben Golang Backend Server...")

	cfgPath := "config.yml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	database, err := db.Init(cfg)
	if err != nil {
		log.Fatalf("Database initialization failed: %v", err)
	}
	log.Println("Database connection established & schema verified.")

	authModule := auth.New(cfg, database)
	taskMgr := tasks.NewManager()
	ollamaSvc := services.NewOllamaService(cfg)
	llmSvc := services.NewLLMService(cfg)
	tileSvc := services.NewTileService(cfg, database, ollamaSvc)
	transSvc := services.NewTranslationService(cfg, database, tileSvc, llmSvc, ollamaSvc, taskMgr)
	mcpServer := mcp.NewServer(cfg, authModule, tileSvc, transSvc, llmSvc, taskMgr)

	r := router.New(cfg, authModule, database, tileSvc, transSvc, llmSvc, taskMgr, mcpServer)

	var listener net.Listener

	if cfg.Server.Socket != "" {
		// Remove stale socket if exists
		_ = os.Remove(cfg.Server.Socket)
		l, err := net.Listen("unix", cfg.Server.Socket)
		if err != nil {
			log.Fatalf("Failed to listen on unix socket '%s': %v", cfg.Server.Socket, err)
		}
		// Allow web server user read/write access
		_ = os.Chmod(cfg.Server.Socket, 0777)
		listener = l
		log.Printf("Listening on Unix Domain Socket: %s\n", cfg.Server.Socket)

		// Clean up socket file on shutdown
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-c
			log.Println("Shutting down... removing unix socket.")
			_ = os.Remove(cfg.Server.Socket)
			os.Exit(0)
		}()
	} else {
		addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			log.Fatalf("Failed to listen on TCP '%s': %v", addr, err)
		}
		listener = l
		log.Printf("Listening on TCP address: http://%s (Port %d)\n", addr, cfg.Server.Port)
	}

	server := &http.Server{
		Handler: r,
	}

	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server stopped unexpectedly: %v", err)
	}
}
