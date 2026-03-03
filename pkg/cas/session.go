package cas

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/juju/persistent-cookiejar"
)

const (
	defaultSessionPath = "data/cookies.json"
)

// newPersistentJar 创建一个新的可持久化 CookieJar，
// 若文件已存在则自动加载先前保存的 Cookie。
func newPersistentJar() (*cookiejar.Jar, error) {
	dir := filepath.Dir(defaultSessionPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建 session 目录失败: %w", err)
	}

	jar, err := cookiejar.New(&cookiejar.Options{
		Filename: defaultSessionPath,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 CookieJar 失败: %w", err)
	}
	return jar, nil
}

// renewSessionCookies 为所有会话级 Cookie（无 Expires）补上过期时间，
// 使 persistent-cookiejar 能将其持久化到文件。
func renewSessionCookies(jar *cookiejar.Jar) {
	expires := time.Now().Add(24 * time.Hour)
	allCookies := jar.AllCookies()

	// 按 Domain 分组，重新写入 jar
	grouped := make(map[string][]*http.Cookie)
	for _, c := range allCookies {
		if c.Expires.IsZero() {
			c.Expires = expires
		}
		grouped[c.Domain] = append(grouped[c.Domain], c)
	}

	for domain, cookies := range grouped {
		scheme := "http"
		for _, c := range cookies {
			if c.Secure {
				scheme = "https"
				break
			}
		}
		u := &url.URL{Scheme: scheme, Host: domain, Path: "/"}
		jar.SetCookies(u, cookies)
	}
}

// SaveSession 将当前 CookieJar 中的所有 Cookie 持久化到文件
func (c *Client) SaveSession() error {
	jar, ok := c.httpClient.Jar.(*cookiejar.Jar)
	if !ok {
		return fmt.Errorf("当前 CookieJar 不支持序列化")
	}

	// 为会话级 Cookie 续命，否则 persistent-cookiejar 不会保存它们
	renewSessionCookies(jar)

	if err := jar.Save(); err != nil {
		return fmt.Errorf("保存 session 失败: %w", err)
	}
	log.Printf("[INFO] Session 已保存到: %s (共 %d 个 Cookie)", defaultSessionPath, len(jar.AllCookies()))
	return nil
}

// LoadSession 从文件加载 Cookie 到当前 CookieJar。
// 返回 true 表示成功加载，false 表示没有可用的 session。
func (c *Client) LoadSession() bool {
	if _, err := os.Stat(defaultSessionPath); err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[WARN] 检查 session 文件失败: %v", err)
		}
		return false
	}

	jar, err := newPersistentJar()
	if err != nil {
		log.Printf("[WARN] 加载 session 失败: %v", err)
		return false
	}

	allCookies := jar.AllCookies()
	if len(allCookies) == 0 {
		log.Printf("[WARN] session 文件存在但无有效 Cookie")
		return false
	}

	c.httpClient.Jar = jar

	log.Printf("[INFO] 已从文件加载 session: %s (共 %d 个 Cookie)", defaultSessionPath, len(allCookies))
	for _, ck := range allCookies {
		log.Printf("[DEBUG] Cookie: %s=%s (Domain=%s, Path=%s, Expires=%s)",
			ck.Name, ck.Value, ck.Domain, ck.Path, ck.Expires.Format("2006-01-02 15:04:05"))
	}
	return true
}

// ClearSession 删除保存的 session 文件
func ClearSession() {
	_ = os.Remove(defaultSessionPath)
}
