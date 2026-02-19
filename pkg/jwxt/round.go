package jwxt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

const (
	BaseURL         = "http://zhjw.qfnu.edu.cn"
	RoundListPath   = "/jsxsd/xsxk/xklc_list"
	RoundViewPath   = "/jsxsd/xsxk/xklc_view"
	RoundIndexPath  = "/jsxsd/xsxk/xsxk_index"
	roundQueryParam = "jx0502zbid"
)

// SelectionRound 表示页面中的一个选课轮次。
type SelectionRound struct {
	ID        string
	EntryPath string
}

// GetSelectionRoundID 通过 DOM 解析轮次页面，提取第一个可进入轮次 ID。
//
// 解析逻辑:
// 1. 定位 #tbKxkc 表格
// 2. 遍历数据行(跳过表头)
// 3. 在每行最后一列中查找文本为“进入选课”的 a 标签
// 4. 提取 href 并解析 jx0502zbid 参数
func GetSelectionRoundID(ctx context.Context, client *http.Client) (string, error) {
	rounds, err := GetSelectionRounds(ctx, client)
	if err != nil {
		return "", err
	}
	if len(rounds) == 0 {
		return "", fmt.Errorf("未在轮次页面中找到可进入的选课链接")
	}
	return rounds[0].ID, nil
}

// GetSelectionRounds 返回按页面顺序提取到的轮次信息。
func GetSelectionRounds(ctx context.Context, client *http.Client) ([]SelectionRound, error) {
	doc, err := fetchRoundDocument(ctx, client)
	if err != nil {
		return nil, err
	}

	table := doc.Find("#tbKxkc").First()
	if table.Length() == 0 {
		return nil, fmt.Errorf("未找到轮次表格: #tbKxkc")
	}

	rounds := make([]SelectionRound, 0)
	seen := make(map[string]struct{})

	table.Find("tr").Each(func(_ int, tr *goquery.Selection) {
		// 跳过表头
		if tr.Find("th").Length() > 0 {
			return
		}

		cells := tr.Find("td")
		if cells.Length() == 0 {
			return
		}

		actionCell := cells.Last()
		actionCell.Find("a").EachWithBreak(func(_ int, a *goquery.Selection) bool {
			if normalizeSpace(a.Text()) != "进入选课" {
				return true
			}

			href, ok := a.Attr("href")
			if !ok {
				return true
			}

			roundID := extractRoundIDFromHref(href)
			if roundID == "" {
				return true
			}
			if _, exists := seen[roundID]; exists {
				return false
			}

			seen[roundID] = struct{}{}
			rounds = append(rounds, SelectionRound{
				ID:        roundID,
				EntryPath: strings.TrimSpace(href),
			})
			return false
		})
	})

	if len(rounds) == 0 {
		return nil, fmt.Errorf("未在 #tbKxkc 中解析到 %s", roundQueryParam)
	}
	return rounds, nil
}

// EnterSelectionRound 进入指定轮次并激活选课上下文。
func EnterSelectionRound(ctx context.Context, client *http.Client, roundID string) error {
	roundID = strings.TrimSpace(roundID)
	if roundID == "" {
		return fmt.Errorf("roundID 不能为空")
	}

	viewURL, err := buildRoundURL(RoundViewPath, roundID)
	if err != nil {
		return err
	}
	indexURL, err := buildRoundURL(RoundIndexPath, roundID)
	if err != nil {
		return err
	}

	// 先访问页面入口，再访问 index 激活会话。
	_ = tryEnterRound(ctx, client, viewURL)

	if err := tryEnterRound(ctx, client, indexURL); err != nil {
		return fmt.Errorf("进入轮次失败: %w", err)
	}
	return nil
}

func fetchRoundDocument(ctx context.Context, client *http.Client) (*goquery.Document, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, BaseURL+RoundListPath, nil)
	if err != nil {
		return nil, fmt.Errorf("创建轮次请求失败: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求轮次列表失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("轮次列表响应异常: %d, body=%q", resp.StatusCode, string(body))
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("解析轮次页面失败: %w", err)
	}
	return doc, nil
}

func extractRoundIDFromHref(href string) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(strings.ToLower(href), "javascript") {
		return ""
	}

	parts := strings.SplitN(href, "?", 2)
	if len(parts) != 2 {
		return ""
	}

	queryPart := parts[1]
	for _, segment := range strings.Split(queryPart, "&") {
		kv := strings.SplitN(segment, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if strings.TrimSpace(kv[0]) != roundQueryParam {
			continue
		}
		return strings.TrimSpace(kv[1])
	}

	return ""
}

func tryEnterRound(ctx context.Context, client *http.Client, rawURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("创建进入轮次请求失败: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("进入轮次请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("进入轮次响应异常: %d, body=%q", resp.StatusCode, string(body))
	}
	return nil
}

func buildRoundURL(path string, roundID string) (string, error) {
	base, err := url.Parse(BaseURL + path)
	if err != nil {
		return "", fmt.Errorf("构造轮次 URL 失败: %w", err)
	}
	query := base.Query()
	query.Set(roundQueryParam, roundID)
	base.RawQuery = query.Encode()
	return base.String(), nil
}

func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}
