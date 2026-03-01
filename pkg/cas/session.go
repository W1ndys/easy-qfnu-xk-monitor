package cas

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultSessionPath = "data/session.json"
)

// PersistentJar 包装标准 CookieJar，自动追踪所有设置过 Cookie 的完整 URL，
// 使得序列化时能获取完整的 session 状态。
type PersistentJar struct {
	jar  *cookiejar.Jar
	mu   sync.RWMutex
	urls map[string]struct{} // 记录所有调用过 SetCookies 的完整 URL
}

// NewPersistentJar 创建一个新的可持久化 CookieJar
func NewPersistentJar() (*PersistentJar, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &PersistentJar{
		jar:  jar,
		urls: make(map[string]struct{}),
	}, nil
}

// SetCookies 实现 http.CookieJar 接口，在委托给内部 jar 的同时记录完整 URL
func (p *PersistentJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	p.jar.SetCookies(u, cookies)
	// 记录完整 URL，保留 scheme + host + path，这样后续 Cookies() 查询时
	// 能通过路径匹配取到所有层级的 Cookie
	full := fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, u.Path)
	p.mu.Lock()
	p.urls[full] = struct{}{}
	p.mu.Unlock()
}

// Cookies 实现 http.CookieJar 接口，直接委托给内部 jar
func (p *PersistentJar) Cookies(u *url.URL) []*http.Cookie {
	return p.jar.Cookies(u)
}

// SerializedCookie 用于 JSON 序列化的 Cookie 结构
type SerializedCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SessionStore 保存完整的 session 信息
type SessionStore struct {
	SavedAt time.Time                     `json:"saved_at"`
	Cookies map[string][]SerializedCookie `json:"cookies"`
}

// SaveSession 将整个 session（CookieJar 中所有已知 URL 的 Cookie）序列化保存到文件
func (c *Client) SaveSession() error {
	pj, ok := c.httpClient.Jar.(*PersistentJar)
	if !ok {
		return fmt.Errorf("当前 CookieJar 不支持序列化")
	}

	store := SessionStore{
		SavedAt: time.Now(),
		Cookies: make(map[string][]SerializedCookie),
	}

	pj.mu.RLock()
	urls := make([]string, 0, len(pj.urls))
	for u := range pj.urls {
		urls = append(urls, u)
	}
	pj.mu.RUnlock()

	// 用每个完整 URL 去查询 Cookie，确保不同路径层级的 Cookie 都能取到
	// 使用 seen 去重（同一个 Cookie 可能被多个 URL 查到）
	type cookieKey struct{ urlStr, name string }
	seen := make(map[cookieKey]struct{})

	for _, rawURL := range urls {
		u, err := url.Parse(rawURL)
		if err != nil {
			continue
		}
		cookies := pj.jar.Cookies(u)
		if len(cookies) == 0 {
			continue
		}

		origin := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
		if store.Cookies[origin] == nil {
			store.Cookies[origin] = make([]SerializedCookie, 0)
		}

		for _, c := range cookies {
			key := cookieKey{origin, c.Name}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			store.Cookies[origin] = append(store.Cookies[origin], SerializedCookie{
				Name:  c.Name,
				Value: c.Value,
			})
		}
	}

	if len(store.Cookies) == 0 {
		return fmt.Errorf("session 为空，无需保存")
	}

	dir := filepath.Dir(defaultSessionPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建 session 目录失败: %w", err)
	}

	content, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 session 失败: %w", err)
	}

	tmpPath := defaultSessionPath + ".tmp"
	if err := os.WriteFile(tmpPath, content, 0o644); err != nil {
		return fmt.Errorf("写入临时 session 文件失败: %w", err)
	}

	if err := os.Rename(tmpPath, defaultSessionPath); err != nil {
		_ = os.Remove(defaultSessionPath)
		if err2 := os.Rename(tmpPath, defaultSessionPath); err2 != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("替换 session 文件失败: %w", err2)
		}
	}

	total := 0
	for _, cs := range store.Cookies {
		total += len(cs)
	}
	log.Printf("[INFO] Session 已保存到: %s (共 %d 个域名, %d 个 Cookie)", defaultSessionPath, len(store.Cookies), total)
	return nil
}

// LoadSession 从文件加载完整的 session 到当前 CookieJar
// 返回 true 表示成功加载，false 表示没有可用的 session
func (c *Client) LoadSession() bool {
	content, err := os.ReadFile(defaultSessionPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[WARN] 读取 session 文件失败: %v", err)
		}
		return false
	}

	var store SessionStore
	if err := json.Unmarshal(content, &store); err != nil {
		log.Printf("[WARN] 解析 session 文件失败: %v", err)
		return false
	}

	if len(store.Cookies) == 0 {
		return false
	}

	pj, err := NewPersistentJar()
	if err != nil {
		log.Printf("[WARN] 创建 PersistentJar 失败: %v", err)
		return false
	}

	loaded := 0
	for origin, serialized := range store.Cookies {
		u, err := url.Parse(origin)
		if err != nil {
			continue
		}
		cookies := make([]*http.Cookie, 0, len(serialized))
		for _, sc := range serialized {
			cookies = append(cookies, &http.Cookie{
				Name:  sc.Name,
				Value: sc.Value,
			})
		}
		pj.SetCookies(u, cookies)
		loaded += len(cookies)
	}

	c.httpClient.Jar = pj
	log.Printf("[INFO] 已从文件加载 session: %d 个域名, %d 个 Cookie (保存于 %s)",
		len(store.Cookies), loaded, store.SavedAt.Format("2006-01-02 15:04:05"))
	return true
}

// ClearSession 删除保存的 session 文件
func ClearSession() {
	_ = os.Remove(defaultSessionPath)
}
