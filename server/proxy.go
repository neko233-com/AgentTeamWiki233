package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

type server struct {
	cfg      *Config
	products map[string]*Product
	cache    *diskCache
	client   *http.Client
	startAt  time.Time

	mu    sync.Mutex
	stats map[string]*productStats
}

type productStats struct {
	Requests  int64 `json:"requests"`
	CacheHits int64 `json:"cacheHits"`
	Bytes     int64 `json:"bytes"`
	Errors    int64 `json:"errors"`
}

var hopHeaders = []string{
	"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func newServer(cfg *Config, cache *diskCache) *server {
	s := &server{
		cfg:      cfg,
		products: map[string]*Product{},
		cache:    cache,
		startAt:  time.Now(),
		stats:    map[string]*productStats{},
	}
	for i := range cfg.Products {
		p := &cfg.Products[i]
		s.products[p.ProductID] = p
	}
	s.client = &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 120 * time.Second,
			ForceAttemptHTTP2:     true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("重定向次数过多")
			}
			if !isGitHubHost(req.URL.Hostname()) {
				return fmt.Errorf("重定向到非 GitHub 域名被拒绝: %s", req.URL.Hostname())
			}
			return nil
		},
	}
	return s
}

func isGitHubHost(host string) bool {
	host = strings.ToLower(strings.Split(host, ":")[0])
	return host == "github.com" || strings.HasSuffix(host, ".github.com") ||
		host == "githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *server) bump(pid string, f func(*productStats)) {
	s.mu.Lock()
	st, ok := s.stats[pid]
	if !ok {
		st = &productStats{}
		s.stats[pid] = st
	}
	f(st)
	s.mu.Unlock()
}

func (s *server) handleStats(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdminToken != "" && r.Header.Get("X-Admin-Token") != s.cfg.AdminToken && r.URL.Query().Get("token") != s.cfg.AdminToken {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "需要 admin token"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"uptimeSeconds": int64(time.Since(s.startAt).Seconds()),
		"cacheBytes":    s.cache.size(),
		"products":      s.stats,
	})
}

// 路由形式：
//
//	/{productId}/{host}/{path...}          如 /demo/raw.githubusercontent.com/owner/repo/main/README.md
//	/{productId}/https://github.com/...    完整 URL 形式（ghproxy 风格）
//	/{productId}/{owner}/{repo}...         省略 host，默认 github.com
func (s *server) handleProxy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if r.URL.Path == "/" {
		s.handleIndex(w, r)
		return
	}

	// 用原始 RequestURI：ServeMux 会把 https:// 的双斜杠清理掉
	rawPath, _, _ := strings.Cut(r.RequestURI, "?")
	raw := strings.TrimPrefix(rawPath, "/")
	pid, rest, _ := strings.Cut(raw, "/")
	p, ok := s.products[pid]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "未知 productId: " + pid})
		return
	}
	if !productAuthorized(p, r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="ghaccel233"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "产品 token 无效"})
		return
	}

	host, upPath, query, err := parseTarget(rest, r.URL.RawQuery)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !hostAllowed(p, host) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "host 不在产品白名单: " + host})
		return
	}
	if !repoAllowed(p, host, upPath) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "仓库不在产品白名单: " + upPath})
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "仅支持 GET/HEAD/POST"})
		return
	}

	upURL := &url.URL{Scheme: "https", Host: host, Path: upPath, RawQuery: query}
	cacheState := "skip"
	status := 0

	defer func() {
		s.bump(pid, func(st *productStats) {
			st.Requests++
			if cacheState == "hit" {
				st.CacheHits++
			}
			if status >= 500 || status == 0 {
				st.Errors++
			}
		})
		log.Printf("[%s] %s %s%s -> %d (%s, %s)", pid, r.Method, host, upPath, status, cacheState, time.Since(start).Round(time.Millisecond))
	}()

	cacheable := r.Method == http.MethodGet && r.Header.Get("Range") == "" && cacheTarget(host, upPath)
	if cacheable {
		key := cacheKey(pid, upURL.String())
		if entry, body, ok := s.cache.get(key); ok {
			cacheState = "hit"
			status = http.StatusOK
			for k, v := range entry.header() {
				w.Header().Set(k, v)
			}
			w.Header().Set("X-Cache", "HIT")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", entry.Size))
			w.WriteHeader(http.StatusOK)
			n, _ := io.Copy(w, body)
			body.Close()
			s.bump(pid, func(st *productStats) { st.Bytes += n })
			return
		}
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, upURL.String(), r.Body)
	if err != nil {
		status = http.StatusBadGateway
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	copyRequestHeaders(req, r, p)

	resp, err := s.client.Do(req)
	if err != nil {
		status = http.StatusBadGateway
		writeJSON(w, status, map[string]string{"error": "上游请求失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	status = resp.StatusCode

	copyResponseHeaders(w, resp)
	w.Header().Set("X-Cache", "MISS")

	var body io.Reader = resp.Body
	var cw *cacheWriter
	if cacheable && resp.StatusCode == http.StatusOK && resp.Header.Get("Content-Encoding") == "" {
		key := cacheKey(pid, upURL.String())
		ttl := cacheTTL(host, upPath)
		cw = s.cache.beginWrite(key, resp.Header, ttl)
		if cw != nil {
			body = io.TeeReader(resp.Body, cw)
			cacheState = "miss-store"
		}
	}

	w.WriteHeader(resp.StatusCode)
	fw := &flushWriter{w: w}
	n, err := io.Copy(fw, body)
	s.bump(pid, func(st *productStats) { st.Bytes += n })
	if cw != nil {
		if err == nil {
			cw.commit()
		} else {
			cw.abort()
		}
	}
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	type item struct {
		ProductID string `json:"productId"`
		Name      string `json:"name"`
		Example   string `json:"example"`
	}
	list := make([]item, 0, len(s.cfg.Products))
	for _, p := range s.cfg.Products {
		list = append(list, item{
			ProductID: p.ProductID,
			Name:      p.Name,
			Example:   fmt.Sprintf("http://%s/%s/github.com/owner/repo.git", r.Host, p.ProductID),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service":  "ghaccel233 (AgentTeamWiki233)",
		"healthz":  "/healthz",
		"products": list,
	})
}

// productAuthorized 支持三种方式：X-Product-Token 头、?token= 查询参数、
// Basic Auth 密码（git 会把 ?query 拼进路径导致查询参数失效，git 场景必须用 Basic Auth）。
func productAuthorized(p *Product, r *http.Request) bool {
	if p.Token == "" {
		return true
	}
	if r.Header.Get("X-Product-Token") == p.Token || r.URL.Query().Get("token") == p.Token {
		return true
	}
	if _, pass, ok := r.BasicAuth(); ok && pass == p.Token {
		return true
	}
	return false
}

func parseTarget(rest, rawQuery string) (host, upPath, query string, err error) {
	if rest == "" {
		return "", "", "", fmt.Errorf("缺少目标地址")
	}
	if strings.HasPrefix(rest, "https://") || strings.HasPrefix(rest, "http://") {
		u, perr := url.Parse(rest)
		if perr != nil {
			return "", "", "", fmt.Errorf("目标 URL 解析失败")
		}
		q, qerr := url.ParseQuery(u.RawQuery)
		if qerr == nil {
			q.Del("token")
			u.RawQuery = q.Encode()
		}
		return u.Host, u.Path, u.RawQuery, nil
	}
	first, tail, _ := strings.Cut(rest, "/")
	if strings.Contains(first, ".") || strings.Contains(first, ":") {
		host = first
		upPath = "/" + tail
	} else {
		host = "github.com"
		upPath = "/" + rest
	}
	q, err := url.ParseQuery(rawQuery)
	if err == nil {
		q.Del("token")
		query = q.Encode()
	}
	if !strings.HasPrefix(upPath, "/") {
		upPath = "/" + upPath
	}
	return host, path.Clean(upPath), query, nil
}

func hostAllowed(p *Product, host string) bool {
	h := strings.ToLower(strings.Split(host, ":")[0])
	for _, a := range p.AllowHosts {
		if strings.ToLower(a) == h {
			return true
		}
	}
	return false
}

func repoAllowed(p *Product, host, upPath string) bool {
	if len(p.AllowRepos) == 1 && p.AllowRepos[0] == "*" {
		return true
	}
	h := strings.ToLower(host)
	if h != "github.com" && h != "codeload.github.com" && h != "raw.githubusercontent.com" && h != "api.github.com" {
		return true
	}
	segs := strings.Split(strings.Trim(upPath, "/"), "/")
	if len(segs) < 2 {
		return true
	}
	repo := segs[0] + "/" + strings.TrimSuffix(segs[1], ".git")
	for _, pattern := range p.AllowRepos {
		if ok, _ := path.Match(pattern, repo); ok {
			return true
		}
	}
	return false
}

func copyRequestHeaders(req *http.Request, r *http.Request, p *Product) {
	for k, vv := range r.Header {
		if isHop(k) || strings.EqualFold(k, "X-Product-Token") || strings.EqualFold(k, "Host") {
			continue
		}
		if strings.EqualFold(k, "Authorization") {
			// 产品 token 走 Basic Auth 时不能透传给上游
			if _, pass, ok := r.BasicAuth(); ok && p.Token != "" && pass == p.Token {
				continue
			}
		}
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	if req.Header.Get("Authorization") == "" && p.GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.GitHubToken)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "ghaccel233/1.0")
	}
}

func copyResponseHeaders(w http.ResponseWriter, resp *http.Response) {
	for k, vv := range resp.Header {
		if isHop(k) {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
}

func isHop(k string) bool {
	for _, h := range hopHeaders {
		if strings.EqualFold(k, h) {
			return true
		}
	}
	return false
}

type flushWriter struct{ w http.ResponseWriter }

func (f *flushWriter) Write(b []byte) (int, error) {
	n, err := f.w.Write(b)
	if fl, ok := f.w.(http.Flusher); ok {
		fl.Flush()
	}
	return n, err
}

func cacheKey(pid, fullURL string) string {
	sum := sha256.Sum256([]byte(pid + "|" + fullURL))
	return hex.EncodeToString(sum[:])
}

func cacheTarget(host, upPath string) bool {
	h := strings.ToLower(host)
	switch h {
	case "raw.githubusercontent.com", "gist.githubusercontent.com", "codeload.github.com", "objects.githubusercontent.com":
		return true
	case "github.com":
		return strings.Contains(upPath, "/releases/download/") || strings.Contains(upPath, "/archive/")
	}
	return false
}

func cacheTTL(host, upPath string) time.Duration {
	h := strings.ToLower(host)
	if h == "objects.githubusercontent.com" || strings.Contains(upPath, "/releases/download/") {
		return 7 * 24 * time.Hour
	}
	if h == "raw.githubusercontent.com" || h == "gist.githubusercontent.com" {
		segs := strings.Split(strings.Trim(upPath, "/"), "/")
		if len(segs) >= 3 && isCommitSHA(segs[2]) {
			return 7 * 24 * time.Hour
		}
		return 10 * time.Minute
	}
	return time.Hour
}

func isCommitSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}