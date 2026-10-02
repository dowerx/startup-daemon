package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"startup-daemon/api"
	"startup-daemon/config"
	"startup-daemon/controller"

	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/moby/moby/client"
)

// main configures the Docker controller and serves the poke API until shutdown.
func main() {
	cfg := config.Get()
	if cfg.PokeToken == "" {
		log.Fatal("POKE_TOKEN must be set")
	}

	dockerApi, err := client.New(client.FromEnv)
	if err != nil {
		panic(err)
	}
	defer dockerApi.Close()

	ctrl := &controller.Controller{APIClient: dockerApi}
	if err := ctrl.LeaseRunning(context.Background(), cfg.LabelPrefix); err != nil {
		log.Fatal(err)
	}
	reapCtx, stopReap := context.WithCancel(context.Background())
	defer stopReap()
	go ctrl.Reap(reapCtx)

	r := gin.Default()
	v1 := r.Group("v1")
	v1.POST("/poke", api.PokeFactory(ctrl, cfg.PokeToken, cfg.LabelPrefix))
	v1.GET("/poke", api.PokeFactory(ctrl, cfg.PokeToken, cfg.LabelPrefix))

	server := &http.Server{Addr: cfg.ListenAddress, Handler: r}
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-signals:
		stopReap()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown failed: %v", err)
		}
	}
}
