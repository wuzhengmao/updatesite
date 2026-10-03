// Command updatesite serves a directory of release artifacts over HTTP.
//
// Besides serving, the binary doubles as the administration tool: it can print
// the upload token of an application and act as its own container health probe,
// so a scratch image needs no shell or extra utilities.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mti/updatesite/internal/buildinfo"
	"github.com/mti/updatesite/internal/config"
	"github.com/mti/updatesite/internal/index"
	"github.com/mti/updatesite/internal/server"
	"github.com/mti/updatesite/internal/token"
)

const usageText = `updatesite - 应用更新站点

用法:
  updatesite                     启动站点
  updatesite token <应用ID>      打印该应用的上传令牌
  updatesite token --list        列出归档目录里所有应用的令牌
  updatesite -healthcheck        探测本机站点是否存活（退出码 0/1）
  updatesite help                显示本帮助

环境变量见 README。`

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-healthcheck", "-health", "--healthcheck":
			os.Exit(healthcheck())
		case "token":
			os.Exit(tokenCommand(os.Args[2:]))
		case "help", "-h", "--help":
			fmt.Println(usageText)
			return
		}
	}
	serve()
}

func serve() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("updatesite: ")

	cfg := config.Load()

	idx := index.New(cfg.DataDir, cfg.CacheDir)
	srv, err := server.New(cfg, idx)
	if err != nil {
		log.Fatalf("cannot start: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go idx.Run(ctx, cfg.ScanInterval)
	go watchHup(ctx, idx)

	httpSrv := newHTTPServer(cfg.Addr, srv.Handler())
	if cfg.TLSEnabled() && cfg.TLSRedirect {
		// Plain HTTP only forwards visitors to the secure listener.
		httpSrv.Handler = server.RedirectToTLS(httpSrv.Handler, cfg)
	}
	servers := []*http.Server{httpSrv}
	listen(httpSrv, "", "")
	log.Printf("listening on %s (data %s, cache %s, rescan every %s, upload %v)",
		cfg.Addr, cfg.DataDir, cfg.CacheDir, cfg.ScanInterval, cfg.UploadEnabled)

	if cfg.TLSEnabled() {
		tlsSrv := newHTTPServer(cfg.TLSAddr, srv.Handler())
		servers = append(servers, tlsSrv)
		listen(tlsSrv, cfg.TLSCert, cfg.TLSKey)
		log.Printf("listening on %s with TLS (certificate %s, redirect %v)",
			cfg.TLSAddr, cfg.TLSCert, cfg.TLSRedirect)
	} else if cfg.TLSRedirect {
		log.Printf("TLS_REDIRECT is on but no certificate is configured, plain HTTP is served as is")
	}

	<-ctx.Done()
	log.Printf("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		if err := s.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown of %s failed: %v", s.Addr, err)
		}
	}
	log.Printf("stopped (version %s, commit %s)", buildinfo.Version, buildinfo.Commit)
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}
}

// listen starts a server in the background. A failure to bind is fatal: running
// without the port the deployment asked for is worse than stopping.
func listen(s *http.Server, cert, key string) {
	go func() {
		var err error
		if cert != "" {
			err = s.ListenAndServeTLS(cert, key)
		} else {
			err = s.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server on %s: %v", s.Addr, err)
		}
	}()
}

// tokenCommand prints upload tokens. It derives them from the application id
// alone, so it works anywhere — no site, no configuration, no database.
//
// Arguments are parsed by hand so that flags may appear before or after the
// application id; the standard flag package stops at the first positional.
func tokenCommand(args []string) int {
	var (
		baseURL string
		quiet   bool
		list    bool
		appID   string
	)
	usage := func() {
		fmt.Fprintln(os.Stderr, "用法: updatesite token [-url https://站点地址] [-q] <应用ID>")
		fmt.Fprintln(os.Stderr, "      updatesite token --list [-q]")
		fmt.Fprintln(os.Stderr, "  -q      只输出令牌本身，便于脚本使用")
		fmt.Fprintln(os.Stderr, "  -url    站点地址，用于打印可直接粘贴的上传命令")
		fmt.Fprintln(os.Stderr, "  -list   列出归档目录里所有应用的令牌")
	}

	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-q", a == "--quiet":
			quiet = true
		case a == "-list", a == "--list":
			list = true
		case a == "-url", a == "--url":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "-url 需要一个值")
				return 2
			}
			i++
			baseURL = args[i]
		case strings.HasPrefix(a, "-url="):
			baseURL = strings.TrimPrefix(a, "-url=")
		case a == "-h", a == "--help", a == "help":
			usage()
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "未知选项 %s\n", a)
			usage()
			return 2
		default:
			if appID != "" {
				fmt.Fprintf(os.Stderr, "多余的参数 %q\n", a)
				return 2
			}
			appID = a
		}
	}

	if list {
		return listTokens(quiet)
	}
	if appID == "" {
		usage()
		return 2
	}
	if quiet {
		fmt.Println(token.For(appID))
		return 0
	}
	printToken(appID, token.For(appID), baseURL)
	return 0
}

func listTokens(quiet bool) int {
	dir := filepath.Join(config.Load().DataDir, "apps")
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法读取 %s: %v\n", dir, err)
		return 1
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			ids = append(ids, e.Name())
		}
	}
	if len(ids) == 0 {
		fmt.Fprintf(os.Stderr, "%s 下没有应用\n", dir)
		return 1
	}
	sort.Strings(ids)
	for _, id := range ids {
		if quiet {
			fmt.Printf("%s\t%s\n", id, token.For(id))
			continue
		}
		fmt.Printf("%-24s %s\n", id, token.For(id))
	}
	return 0
}

func printToken(appID, tok, baseURL string) {
	if baseURL == "" {
		baseURL = "http://<站点地址>"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	fmt.Printf("应用 ID    %s\n", appID)
	fmt.Printf("上传令牌   %s\n\n", tok)
	fmt.Println("把这个令牌交给应用开发人员，他们就能这样发布：")
	fmt.Println()
	fmt.Printf("  curl -fS -X POST \\\n")
	fmt.Printf("    -H \"Authorization: Bearer %s\" \\\n", tok)
	fmt.Printf("    -F \"file=@release.zip\" \\\n")
	fmt.Printf("    -F \"version=1.2.0\" \\\n")
	fmt.Printf("    %s/api/v1/apps/%s/upload\n", baseURL, appID)
	fmt.Println()
	fmt.Println("令牌只由应用 ID 推导，任何环境、任何站点都相同。")
}

// healthcheck calls the local health endpoint. It is used by the container
// HEALTHCHECK, which cannot rely on a shell or curl in a scratch image.
func healthcheck() int {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/api/v1/health")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// watchHup triggers a rescan on SIGHUP so the archive can be refreshed without
// restarting the container. The signal never arrives on Windows, which is fine.
func watchHup(ctx context.Context, idx *index.Index) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
			log.Printf("SIGHUP received, rescanning")
			idx.Rescan()
		}
	}
}
