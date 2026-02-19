package jwxt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	searchPathPrefix = "/jsxsd/xsxkkc/"
)

var (
	// ErrSessionExpired 表示当前会话可能已失效，需要重新登录。
	ErrSessionExpired = errors.New("教务系统会话已失效")

	// ModuleTypes 五个选课搜索模块。
	ModuleTypes = []string{
		"xsxkKnjxk",
		"xsxkBxqjhxk",
		"xsxkXxxk",
		"xsxkFawxk",
		"xsxkGgxxkxk",
	}
)

// CourseInfo 对应选课搜索接口返回的单条课程数据。
type CourseInfo struct {
	Kch      string `json:"kch"`
	Kcmc     string `json:"kcmc"`
	Skls     string `json:"skls"`
	Syrs     string `json:"syrs"`
	Jx0404id string `json:"jx0404id"`
	Jx02id   string `json:"jx02id"`
	Sksj     string `json:"sksj"`
	Xkrs     int    `json:"xkrs"`
	Pkrs     int    `json:"pkrs"`
	Dwmc     string `json:"dwmc"`
	Ktmc     string `json:"ktmc"`
	Skdd     string `json:"skdd"`
}

// UniqueKey 课程唯一标识: {jx02id}_{jx0404id}。
func (c CourseInfo) UniqueKey() string {
	return strings.TrimSpace(c.Jx02id) + "_" + strings.TrimSpace(c.Jx0404id)
}

// SearchResponse 对应搜索接口完整返回。
type SearchResponse struct {
	AaData []CourseInfo `json:"aaData"`
}

// SearchModule 搜索单个模块中的课程信息。
func SearchModule(ctx context.Context, client *http.Client, moduleType string, courseKeyword string) ([]CourseInfo, error) {
	moduleType = strings.TrimSpace(moduleType)
	if moduleType == "" {
		return nil, fmt.Errorf("moduleType 不能为空")
	}

	searchURL, err := url.Parse(BaseURL + searchPathPrefix + moduleType)
	if err != nil {
		return nil, fmt.Errorf("构造搜索 URL 失败: %w", err)
	}

	query := searchURL.Query()
	query.Set("kcxx", strings.TrimSpace(courseKeyword))
	query.Set("skls", "")
	query.Set("sfym", "true")
	query.Set("sfct", "false")
	query.Set("sfxx", "false")
	searchURL.RawQuery = query.Encode()

	form := url.Values{}
	form.Set("iDisplayStart", "0")
	form.Set("iDisplayLength", "10000")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, searchURL.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("创建搜索请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", BaseURL+searchPathPrefix+moduleType)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求搜索接口失败[%s]: %w", moduleType, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently ||
		resp.StatusCode == http.StatusTemporaryRedirect || resp.StatusCode == http.StatusPermanentRedirect {
		return nil, fmt.Errorf("%w: 搜索接口发生重定向[%s]", ErrSessionExpired, moduleType)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("搜索接口响应异常[%s]: %d, body=%q", moduleType, resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取搜索响应失败[%s]: %w", moduleType, err)
	}
	if looksLikeLoginHTML(body) {
		return nil, fmt.Errorf("%w: 搜索接口返回登录页[%s]", ErrSessionExpired, moduleType)
	}

	var result SearchResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&result); err != nil {
		if looksLikeLoginHTML(body) {
			return nil, fmt.Errorf("%w: 搜索响应非 JSON[%s]", ErrSessionExpired, moduleType)
		}
		return nil, fmt.Errorf("解析搜索响应失败[%s]: %w", moduleType, err)
	}

	return result.AaData, nil
}

// SearchAllModules 搜索全部五个模块并按唯一键去重。
func SearchAllModules(ctx context.Context, client *http.Client, courseKeyword string) ([]CourseInfo, error) {
	uniq := make(map[string]CourseInfo)
	for _, moduleType := range ModuleTypes {
		courses, err := SearchModule(ctx, client, moduleType, courseKeyword)
		if err != nil {
			return nil, err
		}
		for _, course := range courses {
			uniq[course.UniqueKey()] = course
		}

		// 降低接口压力，避免单轮请求过于密集。
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	result := make([]CourseInfo, 0, len(uniq))
	for _, course := range uniq {
		result = append(result, course)
	}
	return result, nil
}

// IsSessionExpired 判断错误是否由会话失效触发。
func IsSessionExpired(err error) bool {
	return errors.Is(err, ErrSessionExpired)
}

func looksLikeLoginHTML(body []byte) bool {
	text := strings.ToLower(strings.TrimSpace(string(body)))
	if text == "" {
		return false
	}
	return strings.HasPrefix(text, "<!doctype html") ||
		strings.HasPrefix(text, "<html") ||
		strings.Contains(text, "authserver/login") ||
		strings.Contains(text, "统一身份认证") ||
		strings.Contains(text, "教学一体化服务平台")
}
