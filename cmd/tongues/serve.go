package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nazboyko/channel-tongues/internal/config"
	"github.com/nazboyko/channel-tongues/internal/server"
	"github.com/nazboyko/channel-tongues/internal/translate"
)

func runServe(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	mock := fs.Bool("mock", false, "sample videos and recorded translations, no YouTube and no Ollama (for trying the panel)")
	quitIdle := fs.Bool("quit-when-idle", false, "stop after two hours without a request (used when the panel starts the helper)")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	a, err := newApp(cfg, *mock)
	if err != nil {
		return err
	}
	defer a.Close()
	code, err := server.LoadPairing(a.DataDir)
	if err != nil {
		return err
	}
	srv := server.New(a.Service, code, cfg.Port)
	ln, err := srv.Listen()
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return fmt.Errorf("another helper is already running on %s; keep using it, or stop it first", cfg.Addr())
		}
		return err
	}
	var lastRequest atomic.Int64
	lastRequest.Store(time.Now().Unix())
	handler := srv.Handler()
	httpSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			lastRequest.Store(time.Now().Unix())
			handler.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	model := a.ModelHealth(ctx)

	if *mock {
		fmt.Println("Channel Tongues helper is running with sample videos (no YouTube, no Ollama).")
	} else {
		fmt.Println("Channel Tongues helper is running.")
	}
	fmt.Printf("Pairing code: %s  (the side panel asks for it once)\n", code)
	switch model.State {
	case translate.ModelReady:
		fmt.Printf("Model: %s, ready.\n", model.Model)
	case translate.ModelMissing:
		fmt.Printf("Model: %s is not downloaded yet. Run: ollama pull %s\n", model.Model, model.Model)
	default:
		fmt.Println("Ollama is not running. Start the Ollama app, or run: ollama serve")
	}
	if g := cfg.GuardLine(); g != "" && !*mock {
		fmt.Println(g)
	}
	fmt.Println("Leave this window open while you work in Studio. Press Ctrl+C to stop.")
	a.Log.Info("helper started", "addr", cfg.Addr(), "mock", *mock, "model", model.Model)

	errc := make(chan error, 1)
	go func() { errc <- httpSrv.Serve(ln) }()
	idle := make(chan struct{})
	if *quitIdle {
		go func() {
			for range time.Tick(time.Minute) {
				if time.Since(time.Unix(lastRequest.Load(), 0)) > 2*time.Hour {
					close(idle)
					return
				}
			}
		}()
	}
	select {
	case <-idle:
		a.Log.Info("helper stopped after two idle hours")
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutdown)
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutdown)
		fmt.Println("\nHelper stopped.")
		return nil
	case err := <-errc:
		return err
	}
}
