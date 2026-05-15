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
	"strings"
	"syscall"
	"time"

	"github.com/qiangli/omniscore/frontend"
	"github.com/qiangli/omniscore/internal/admin"
	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/server"
	"github.com/qiangli/omniscore/internal/store"
	syncpkg "github.com/qiangli/omniscore/internal/sync"
)

func main() {
	bind := flag.String("bind", "0.0.0.0:28080", "host:port to bind the HTTP server")
	dbPath := flag.String("db", "omniscore.db", "SQLite database path")
	contentRoot := flag.String("content", "content", "directory containing per-exam-type subdirs (sat/, ap/, ...) each with per-slug subdirs holding test.json/curve.json/figures/, OR a flat tests/+curves/ layout. Accepts ~ for $HOME.")
	keyPath := flag.String("key", "omniscore.key", "HMAC cookie signing key file (auto-created). Accepts ~ for $HOME.")
	adminKeyPath := flag.String("admin-key", "omniscore.admin-key", "Admin passphrase file (auto-created on first boot; passphrase printed once in the banner). Accepts ~ for $HOME.")
	syncMode := flag.String("sync", "noop", "External sync adapter: noop (default; outbox accumulates locally) or a future named adapter.")
	flag.Parse()

	*dbPath = expandHome(*dbPath)
	*contentRoot = expandHome(*contentRoot)
	*keyPath = expandHome(*keyPath)
	*adminKeyPath = expandHome(*adminKeyPath)

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

	if err := content.LoadFromDisk(ctx, s, *contentRoot); err != nil {
		logger.Error("load content", "err", err)
		os.Exit(1)
	}
	logger.Info("content loaded", "root", *contentRoot)

	auth, passphrase, err := admin.NewAuth(*adminKeyPath)
	if err != nil {
		logger.Error("admin auth init", "err", err)
		os.Exit(1)
	}

	srv, err := server.New(s, *keyPath, frontend.DistFS(), *contentRoot, auth, logger)
	if err != nil {
		logger.Error("init server", "err", err)
		os.Exit(1)
	}

	adapter := pickAdapter(*syncMode)
	if adapter != nil {
		go syncpkg.Run(ctx, syncpkg.Config{
			Adapter: adapter,
			DB:      s.DB,
			PullEntities: []string{
				syncpkg.EntityUser,
				syncpkg.EntityStandardTest,
				syncpkg.EntityTask,
			},
		})
	}

	httpSrv := &http.Server{
		Addr:              *bind,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	printJoinInfo(logger, *bind, passphrase)

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

func pickAdapter(name string) syncpkg.Adapter {
	switch name {
	case "", "noop":
		return syncpkg.Noop{}
	default:
		// Unknown adapter falls back to noop with a log line in Run().
		return syncpkg.Noop{}
	}
}

func printJoinInfo(logger *slog.Logger, bind, adminPassphrase string) {
	_, port, err := net.SplitHostPort(bind)
	if err != nil {
		port = "28080"
	}
	ips := lanIPv4s()
	if len(ips) == 0 {
		ips = []string{"127.0.0.1"}
	}
	logger.Info("listening", "bind", bind)

	primary := fmt.Sprintf("http://%s:%s", ips[0], port)
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "  OmniScore is ready.")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "  Landing page: %s\n", primary)
	if len(ips) > 1 {
		fmt.Fprintln(os.Stdout, "  Other addresses:")
		for _, ip := range ips[1:] {
			fmt.Fprintf(os.Stdout, "    http://%s:%s\n", ip, port)
		}
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "  Admin login: %s/admin/login\n", primary)
	fmt.Fprintf(os.Stdout, "  Admin passphrase (printed once): %s\n", adminPassphrase)
	fmt.Fprintln(os.Stdout)
}

// expandHome rewrites a leading "~" or "~/" to the current user's home dir
// so users can pass -content ~/omni-data without manual $HOME expansion. A
// failed lookup leaves the path unchanged.
func expandHome(p string) string {
	if p == "" || (p[0] != '~') {
		return p
	}
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if len(p) >= 2 && p[1] == '/' {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
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
