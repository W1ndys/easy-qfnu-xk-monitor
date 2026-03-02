package cas

import (
	"bytes"
	"encoding/gob"
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
	defaultSessionPath = "data/session.gob"
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

// gobCookie 用于 gob 序列化的 Cookie 结构，保存完整属性
type gobCookie struct {
	Name     string
	Value    string
	Path     string
	Domain   string
	Secure   bool
	HttpOnly bool
}

// gobEntry 按完整 URL 分组的 Cookie 条目
type gobEntry struct {
	URL     string // 完整 URL（含路径），如 http://zhjw.qfnu.edu.cn/jsxsd/
	Cookies []gobCookie
}

// gobSessionStore gob 序列化的 session 完整结构
type gobSessionStore struct {
	SavedAt time.Time
	Entries []gobEntry
}

// SaveSession 将整个 session（CookieJar 中所有已知 URL 的 Cookie）序列化保存到文件
func (c *Client) SaveSession() error {
	pj, ok := c.httpClient.Jar.(*PersistentJar)
	if !ok {
		return fmt.Errorf("当前 CookieJar 不支持序列化")
	}

	pj.mu.RLock()
	urls := make([]string, 0, len(pj.urls))
	for u := range pj.urls {
		urls = append(urls, u)
	}
	pj.mu.RUnlock()

	store := gobSessionStore{
		SavedAt: time.Now(),
	}

	// 按完整 URL 分组保存，不做 origin 归并，保留路径信息
	// 使用 seen 对 (url, name) 去重，避免子路径查询到父路径 Cookie 导致重复
	type cookieKey struct{ url, name string }
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

		entry := gobEntry{URL: rawURL}
		for _, ck := range cookies {
			key := cookieKey{rawURL, ck.Name}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			entry.Cookies = append(entry.Cookies, gobCookie{
				Name:     ck.Name,
				Value:    ck.Value,
				Path:     ck.Path,
				Domain:   ck.Domain,
				Secure:   ck.Secure,
				HttpOnly: ck.HttpOnly,
			})
		}
		if len(entry.Cookies) > 0 {
			store.Entries = append(store.Entries, entry)
		}
	}

	if len(store.Entries) == 0 {
		return fmt.Errorf("session 为空，无需保存")
	}

	dir := filepath.Dir(defaultSessionPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建 session 目录失败: %w", err)
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(store); err != nil {
		return fmt.Errorf("序列化 session 失败: %w", err)
	}

	tmpPath := defaultSessionPath + ".tmp"
	if err := os.WriteFile(tmpPath, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("写入临时 session 文件失败: %w", err)
	}

	if err := os.Rename(tmpPath, defaultSessionPath); err != nil {
		_ = os.Remove(defaultSessionPath)
		if err2 := os.Rename(tmpPath, defaultSessionPath); err2 != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("替换 session 文件失败: %w", err2)
		}
	}

	totalCookies := 0
	urlCount := len(store.Entries)
	for _, e := range store.Entries {
		totalCookies += len(e.Cookies)
	}
	log.Printf("[INFO] Session 已保存到: %s (共 %d 个 URL, %d 个 Cookie)", defaultSessionPath, urlCount, totalCookies)
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

	var store gobSessionStore
	if err := gob.NewDecoder(bytes.NewReader(content)).Decode(&store); err != nil {
		log.Printf("[WARN] 解析 session 文件失败: %v", err)
		return false
	}

	if len(store.Entries) == 0 {
		return false
	}

	pj, err := NewPersistentJar()
	if err != nil {
		log.Printf("[WARN] 创建 PersistentJar 失败: %v", err)
		return false
	}

	loaded := 0
	for _, entry := range store.Entries {
		u, err := url.Parse(entry.URL)
		if err != nil {
			continue
		}
		cookies := make([]*http.Cookie, 0, len(entry.Cookies))
		for _, gc := range entry.Cookies {
			cookies = append(cookies, &http.Cookie{
				Name:     gc.Name,
				Value:    gc.Value,
				Path:     gc.Path,
				Domain:   gc.Domain,
				Secure:   gc.Secure,
				HttpOnly: gc.HttpOnly,
			})
		}
		pj.SetCookies(u, cookies)
		loaded += len(cookies)
	}

	c.httpClient.Jar = pj
	log.Printf("[INFO] 已从文件加载 session: %d 个 URL, %d 个 Cookie (保存于 %s)",
		len(store.Entries), loaded, store.SavedAt.Format("2006-01-02 15:04:05"))
	return true
}

// ClearSession 删除保存的 session 文件
func ClearSession() {
	_ = os.Remove(defaultSessionPath)
}
