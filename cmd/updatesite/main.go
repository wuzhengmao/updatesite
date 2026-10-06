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

	// The scratch image ships no zoneinfo, so the time zone database is embedded
	// and TZ can take effect.
	_ "time/tzdata"

	"github.com/wuzhengmao/updatesite/internal/buildinfo"
	"github.com/wuzhengmao/updatesite/internal/config"
	"github.com/wuzhengmao/updatesite/internal/downloads"
	"github.com/wuzhengmao/updatesite/internal/index"
	"github.com/wuzhengmao/updatesite/internal/server"
	"github.com/wuzhengmao/updatesite/internal/token"
)

const usageText = `updatesite - 应用更新站点

用法:
  updatesite                     启动站点
  updatesite serve               同上
  updatesite token <应用ID>      打印该应用的上传令牌
  updatesite token -gen-secret   生成一个新的 UPLOAD_SECRET
  updatesite token --list        列出归档目录里所有应用的令牌
  updatesite -healthcheck        探测本机站点是否存活（退出码 0/1）
  updatesite help                显示本帮助

在容器里运行时，镜像的 ENTRYPOINT 已经是本程序，不要再写一遍程序名：

  docker run --rm <镜像> token -gen-secret      # 对
  docker run --rm <镜像> updatesite token ...   # 错，会变成给程序传一个参数

环境变量见 README。`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		serve()
		return
	}

	switch args[0] {
	case "-healthcheck", "-health", "--healthcheck":
		os.Exit(healthcheck())
	case "token":
		os.Exit(tokenCommand(args[1:]))
	case "serve":
		serve()
	case "help", "-h", "--help":
		fmt.Println(usageText)
	default:
		// Never fall through to serving: a mistyped subcommand silently
		// becoming a long running server is baffling to debug, and in a
		// container it looks like the command simply hung.
		fmt.Fprintf(os.Stderr, "未知命令 %q\n\n%s\n", args[0], usageText)
		os.Exit(2)
	}
}

func serve() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("updatesite: ")

	cfg := config.Load()

	idx := index.New(cfg.DataDir, cfg.CacheDir)
	dl := downloads.New(cfg.DataDir)
	srv, err := server.New(cfg, idx, dl)
	if err != nil {
		log.Fatalf("cannot start: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go idx.Run(ctx, cfg.ScanInterval)
	go dl.Run(ctx, downloads.FlushInterval)
	go watchHup(ctx, idx)

	// Settle whether HTTPS can run *before* wiring the HTTP handler: a
	// redirect to a port that never came up would make the site unreachable.
	// Loading the pair up front also turns a bad path or a mismatched key into
	// a plain message instead of a crash.
	var tlsPair *tls.Certificate
	if cfg.TLSEnabled() {
		pair, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			log.Printf("TLS is configured but unusable, serving plain HTTP only: %v", err)
			log.Printf("  TLS_CERT=%s", cfg.TLSCert)
			log.Printf("  TLS_KEY=%s", cfg.TLSKey)
		} else {
			tlsPair = &pair
		}
	}

	httpHandler := srv.Handler()
	redirect := tlsPair != nil && cfg.TLSRedirect
	if redirect {
		httpHandler = server.RedirectToTLS(httpHandler, cfg)
	}

	httpSrv := newHTTPServer(cfg.Addr, httpHandler)
	servers := []*http.Server{httpSrv}
	listen(httpSrv, false)
	log.Printf("listening on %s (data %s, cache %s, rescan every %s, upload %v)",
		cfg.Addr, cfg.DataDir, cfg.CacheDir, cfg.ScanInterval, cfg.UploadEnabled)
	log.Printf("download counts are kept in %s", dl.Path())
	// Say where absolute URLs come from. A BASE_URL left over from a local test
	// is otherwise invisible until someone notices the links are wrong.
	if cfg.BaseURL != "" {
		log.Printf("absolute URLs are built from BASE_URL=%s", cfg.BaseURL)
	} else {
		log.Printf("absolute URLs follow X-Forwarded-Proto/Host, or the request host when absent")
	}

	if tlsPair != nil {
		tlsSrv := newHTTPServer(cfg.TLSAddr, srv.Handler())
		tlsSrv.TLSConfig.Certificates = []tls.Certificate{*tlsPair}
		servers = append(servers, tlsSrv)
		listen(tlsSrv, true)
		log.Printf("listening on %s with TLS (certificate %s, %d cert(s) in the chain, redirect to HTTPS %v)",
			cfg.TLSAddr, cfg.TLSCert, len(tlsPair.Certificate), redirect)
	} else if cfg.TLSRedirect {
		log.Printf("TLS_REDIRECT is ignored because HTTPS did not start")
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
	// After the drain: a request that was already in flight may have counted.
	dl.Save()
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
//
// The certificate is already in TLSConfig, so ListenAndServeTLS is called with
// empty file names.
func listen(s *http.Server, useTLS bool) {
	go func() {
		var err error
		if useTLS {
			err = s.ListenAndServeTLS("", "")
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
		baseURL   string
		quiet     bool
		list      bool
		genSecret bool
		appID     string
	)
	usage := func() {
		fmt.Fprintln(os.Stderr, "用法: updatesite token [-url https://站点地址] [-q] <应用ID>")
		fmt.Fprintln(os.Stderr, "      updatesite token --list [-q]")
		fmt.Fprintln(os.Stderr, "      updatesite token -gen-secret")
		fmt.Fprintln(os.Stderr, "  -q           只输出令牌本身，便于脚本使用")
		fmt.Fprintln(os.Stderr, "  -url         站点地址，用于打印可直接粘贴的上传命令")
		fmt.Fprintln(os.Stderr, "  -list        列出归档目录里所有应用的令牌")
		fmt.Fprintln(os.Stderr, "  -gen-secret  生成一个新的 UPLOAD_SECRET")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "令牌由 UPLOAD_SECRET 与应用 ID 共同推导；各环境使用同一个密钥，")
		fmt.Fprintln(os.Stderr, "同一个应用的令牌就处处相同。")
	}

	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-q", a == "--quiet":
			quiet = true
		case a == "-list", a == "--list":
			list = true
		case a == "-gen-secret", a == "--gen-secret":
			genSecret = true
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

	if genSecret {
		secret, err := token.NewSecret()
		if err != nil {
			fmt.Fprintf(os.Stderr, "无法生成密钥: %v\n", err)
			return 1
		}
		if quiet {
			fmt.Println(secret)
			return 0
		}
		fmt.Printf("新的上传密钥:\n\n  %s\n\n", secret)
		fmt.Println("把它配置到站点上（各环境用同一个值，同一应用的令牌就处处相同）：")
		fmt.Println()
		fmt.Println("  docker-compose.yml  →  UPLOAD_SECRET: \"" + secret + "\"")
		fmt.Println("  或   UPLOAD_SECRET=" + secret + " docker compose up -d")
		fmt.Println()
		fmt.Println("这是唯一的副本，请自行保存；换掉它会让已发出的令牌全部失效。")
		return 0
	}

	secret := strings.TrimSpace(os.Getenv("UPLOAD_SECRET"))
	if secret == "" {
		fmt.Fprintln(os.Stderr, "未设置 UPLOAD_SECRET，无法推导令牌。")
		fmt.Fprintln(os.Stderr, "先生成一个：updatesite token -gen-secret")
		fmt.Fprintln(os.Stderr, "再带上它运行：UPLOAD_SECRET=<密钥> updatesite token <应用ID>")
		return 2
	}

	if list {
		return listTokens(quiet, secret)
	}
	if appID == "" {
		usage()
		return 2
	}
	if quiet {
		fmt.Println(token.For(secret, appID))
		return 0
	}
	printToken(appID, token.For(secret, appID), baseURL)
	return 0
}

func listTokens(quiet bool, secret string) int {
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
			fmt.Printf("%s\t%s\n", id, token.For(secret, id))
			continue
		}
		fmt.Printf("%-24s %s\n", id, token.For(secret, id))
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
