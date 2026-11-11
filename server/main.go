package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

type Config struct {
	Listen      string    `json:"listen"`
	CacheDir    string    `json:"cacheDir"`
	CacheMaxMB  int64     `json:"cacheMaxMB"`
	AdminToken  string    `json:"adminToken"`
	Products    []Product `json:"products"`
}

type Product struct {
	ProductID   string   `json:"productId"`
	Name        string   `json:"name"`
	Token       string   `json:"token"`
	GitHubToken string   `json:"githubToken"`
	AllowHosts  []string `json:"allowHosts"`
	AllowRepos  []string `json:"allowRepos"`
}

var defaultHosts = []string{
	"github.com",
	"raw.githubusercontent.com",
	"api.github.com",
	"codeload.github.com",
	"objects.githubusercontent.com",
	"gist.github.com",
	"gist.githubusercontent.com",
	"uploads.github.com",
}

var productIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func defaultConfig() *Config {
	return &Config{
		Listen:     ":8233",
		CacheDir:   "cache",
		CacheMaxMB: 2048,
		Products: []Product{{
			ProductID:  "default",
			Name:       "默认产品",
			AllowHosts: defaultHosts,
			AllowRepos: []string{"*"},
		}},
	}
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg := defaultConfig()
		buf, _ := json.MarshalIndent(cfg, "", "  ")
		if werr := os.WriteFile(path, buf, 0o600); werr != nil {
			return nil, fmt.Errorf("生成默认配置失败: %w", werr)
		}
		log.Printf("未找到配置，已生成默认配置: %s（默认 productId=default）", path)
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	cfg := defaultConfig()
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}) // Windows 记事本等会写入 UTF-8 BOM
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	if cfg.Listen == "" {
		cfg.Listen = ":8233"
	}
	if cfg.CacheDir == "" {
		cfg.CacheDir = "cache"
	}
	if len(cfg.Products) == 0 {
		return nil, fmt.Errorf("配置中至少需要一个 product")
	}
	seen := map[string]bool{}
	for i := range cfg.Products {
		p := &cfg.Products[i]
		if !productIDRe.MatchString(p.ProductID) {
			return nil, fmt.Errorf("非法 productId: %q（只允许字母/数字/_/-，最长 64）", p.ProductID)
		}
		if seen[p.ProductID] {
			return nil, fmt.Errorf("productId 重复: %s", p.ProductID)
		}
		seen[p.ProductID] = true
		if len(p.AllowHosts) == 0 {
			p.AllowHosts = defaultHosts
		}
		if len(p.AllowRepos) == 0 {
			p.AllowRepos = []string{"*"}
		}
	}
	return cfg, nil
}

func main() {
	configPath := flag.String("config", "config.json", "配置文件路径")
	listen := flag.String("listen", "", "覆盖配置中的监听地址，如 :8233")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("配置错误: %v", err)
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if !filepath.IsAbs(cfg.CacheDir) {
		if abs, err := filepath.Abs(cfg.CacheDir); err == nil {
			cfg.CacheDir = abs
		}
	}

	cache, err := newDiskCache(cfg.CacheDir, cfg.CacheMaxMB*1024*1024)
	if err != nil {
		log.Fatalf("初始化缓存失败: %v", err)
	}

	srv := newServer(cfg, cache)
	// 不用 ServeMux：它会把完整 URL 形式里的双斜杠清理掉并 307 重定向
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, "ok")
		case "/admin/stats":
			srv.handleStats(w, r)
		default:
			srv.handleProxy(w, r)
		}
	})

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Println("收到退出信号，正在关闭…")
		httpSrv.Close()
	}()

	log.Printf("ghaccel233 启动，监听 %s，产品数 %d，缓存目录 %s", cfg.Listen, len(cfg.Products), cfg.CacheDir)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("服务退出: %v", err)
	}
}