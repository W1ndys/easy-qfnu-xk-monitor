package cas

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

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

// SaveSession 将当前 CookieJar 中的所有 Cookie 持久化到文件
func (c *Client) SaveSession() error {
	jar, ok := c.httpClient.Jar.(*cookiejar.Jar)
	if !ok {
		return fmt.Errorf("当前 CookieJar 不支持序列化")
	}
	if err := jar.Save(); err != nil {
		return fmt.Errorf("保存 session 失败: %w", err)
	}
	log.Printf("[INFO] Session 已保存到: %s", defaultSessionPath)
	return nil
}

// LoadSession 从文件加载 Cookie 到当前 CookieJar。
// persistent-cookiejar 在 New 时已自动加载，此方法检测文件是否存在
// 并重新创建 jar 以加载最新内容。
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

	c.httpClient.Jar = jar
	log.Printf("[INFO] 已从文件加载 session: %s", defaultSessionPath)
	return true
}

// ClearSession 删除保存的 session 文件
func ClearSession() {
	_ = os.Remove(defaultSessionPath)
}
