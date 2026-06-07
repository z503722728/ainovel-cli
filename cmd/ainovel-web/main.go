package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/logger"
	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/web"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	addr, configPath, outputDir, style, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ainovel-web: %v\n", err)
		os.Exit(1)
	}

	if bootstrap.NeedsSetup(configPath) {
		setupCfg, err := bootstrap.RunSetup()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ainovel-web: setup: %v\n", err)
			os.Exit(1)
		}
		runServer(setupCfg, addr, outputDir, style)
		return
	}

	cfg, err := bootstrap.LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ainovel-web: config: %v\n", err)
		os.Exit(1)
	}

	runServer(cfg, addr, outputDir, style)
}

func runServer(cfg bootstrap.Config, addr, outputDir, style string) {
	rules.EnsureHomeRulesDir()

	// 尽早检查单实例锁，避免加载模型后才报错
	if err := web.CheckLock(addr); err != nil {
		fmt.Fprintf(os.Stderr, "ainovel-web: %v\n", err)
		os.Exit(1)
	}

	if outputDir != "" {
		cfg.OutputDir = outputDir
	}
	if style != "" {
		cfg.Style = style
	}

	bundle := assets.Load(cfg.Style)
	rt, err := host.New(cfg, bundle)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ainovel-web: create host: %v\n", err)
		os.Exit(1)
	}
	cleanup := logger.SetupFile(rt.Dir(), "web.log", false)
	defer cleanup()
	defer rt.Close()

	srv, err := web.NewServer(rt, addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ainovel-web: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := srv.Serve(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "ainovel-web: server: %v\n", err)
		os.Exit(1)
	}
}

func parseArgs(argv []string) (addr, configPath, outputDir, style string, err error) {
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--web", "-w":
			if i+1 < len(argv) && !isFlag(argv[i+1]) {
				addr = argv[i+1]
				i++
			} else {
				addr = "0.0.0.0:8080"
			}
		case "--config", "-c":
			if i+1 >= len(argv) {
				return "", "", "", "", fmt.Errorf("--config 缺少值")
			}
			configPath = argv[i+1]
			i++
		case "--output", "-o":
			if i+1 >= len(argv) {
				return "", "", "", "", fmt.Errorf("--output 缺少值")
			}
			outputDir = argv[i+1]
			i++
		case "--style", "-s":
			if i+1 >= len(argv) {
				return "", "", "", "", fmt.Errorf("--style 缺少值")
			}
			style = argv[i+1]
			i++
		default:
			if strings.HasPrefix(argv[i], "-") {
				return "", "", "", "", fmt.Errorf("未知参数: %s", argv[i])
			}
		}
	}
	if addr == "" {
		addr = "0.0.0.0:8080"
	}
	return
}

func isFlag(s string) bool {
	return len(s) > 0 && s[0] == '-'
}
