// Command omniscore boots the practice-test server for one classroom.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	qrterminal "github.com/mdp/qrterminal/v3"

	"github.com/qiangli/omniscore/frontend"
	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/server"
	"github.com/qiangli/omniscore/internal/store"
)

func main() {
	bind := flag.String("bind", "0.0.0.0:8080", "host:port to bind the HTTP server")
	dbPath := flag.String("db", "omniscore.db", "SQLite database path")
	contentRoot := flag.String("content", "content", "directory containing tests/ and curves/ subdirectories")
	keyPath := flag.String("key", "omniscore.key", "HMAC cookie signing key file (auto-created)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		logger.Error("open store", "err", err)
		os.Exit(1)
	}
	defer s.Close()
	logger.Info("store ready", "path", *dbPath)

	testsDir := filepath.Join(*contentRoot, "tests")
	curvesDir := filepath.Join(*contentRoot, "curves")
	if err := content.LoadFromDisk(ctx, s, testsDir, curvesDir); err != nil {
		logger.Error("load content", "err", err)
		os.Exit(1)
	}
	logger.Info("content loaded", "tests_dir", testsDir, "curves_dir", curvesDir)

	srv, err := server.New(s, *keyPath, frontend.DistFS(), logger)
	if err != nil {
		logger.Error("init server", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              *bind,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	printJoinInfo(logger, *bind)

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("listen", "err", err)
			cancel()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	_ = httpSrv.Shutdown(shutCtx)
}

func printJoinInfo(logger *slog.Logger, bind string) {
	_, port, err := net.SplitHostPort(bind)
	if err != nil {
		port = "8080"
	}
	ips := lanIPv4s()
	if len(ips) == 0 {
		ips = []string{"127.0.0.1"}
	}
	logger.Info("listening", "bind", bind)
	for _, ip := range ips {
		url := fmt.Sprintf("http://%s:%s", ip, port)
		logger.Info("join URL", "url", url)
	}
	primary := fmt.Sprintf("http://%s:%s", ips[0], port)
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "Scan to join:")
	qrterminal.GenerateWithConfig(primary, qrterminal.Config{
		Level:     qrterminal.L,
		Writer:    os.Stdout,
		BlackChar: qrterminal.BLACK,
		WhiteChar: qrterminal.WHITE,
		QuietZone: 1,
	})
	fmt.Fprintln(os.Stdout, "")
}

func lanIPv4s() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil {
			continue
		}
		// Skip link-local 169.254.x.x.
		if strings.HasPrefix(ip4.String(), "169.254.") {
			continue
		}
		out = append(out, ip4.String())
	}
	return out
}
