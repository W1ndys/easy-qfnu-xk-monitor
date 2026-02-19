package notify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/W1ndys/easy-qfnu-xk-monitor/pkg/jwxt"
)

type sendGroupMsgRequest struct {
	GroupID int64  `json:"group_id"`
	Message string `json:"message"`
}

type onebotResponse struct {
	Status  string `json:"status"`
	RetCode int    `json:"retcode"`
	Message string `json:"message"`
}

// Notifier 封装 OneBot HTTP 推送能力。
type Notifier struct {
	oneBotURL   string
	oneBotToken string
	groupList   []string
	client      *http.Client
}

// NewNotifier 创建 OneBot 推送器。
func NewNotifier(oneBotURL, oneBotToken string, groupList []string, client *http.Client) *Notifier {
	return &Notifier{
		oneBotURL:   strings.TrimRight(strings.TrimSpace(oneBotURL), "/"),
		oneBotToken: strings.TrimSpace(oneBotToken),
		groupList:   append([]string(nil), groupList...),
		client:      client,
	}
}

// SendGroupMessage 发送单条群消息。
func (n *Notifier) SendGroupMessage(groupID string, message string) error {
	gid, err := strconv.ParseInt(strings.TrimSpace(groupID), 10, 64)
	if err != nil {
		return fmt.Errorf("群号格式错误[%s]: %w", groupID, err)
	}

	payload := sendGroupMsgRequest{
		GroupID: gid,
		Message: message,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化 OneBot 请求失败: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, n.oneBotURL+"/send_group_msg", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建 OneBot 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if n.oneBotToken != "" {
		req.Header.Set("Authorization", "Bearer "+n.oneBotToken)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("请求 OneBot 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("OneBot 响应异常: %d", resp.StatusCode)
	}

	var result onebotResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil
	}
	if result.RetCode != 0 && result.Status != "ok" {
		return fmt.Errorf("OneBot 返回失败: retcode=%d, message=%s", result.RetCode, result.Message)
	}
	return nil
}

// BroadcastMessage 向配置中的所有群发送同一条消息。
func (n *Notifier) BroadcastMessage(message string) error {
	var allErr error
	for _, groupID := range n.groupList {
		if err := n.SendGroupMessage(groupID, message); err != nil {
			allErr = errors.Join(allErr, fmt.Errorf("群[%s]发送失败: %w", groupID, err))
		}
	}
	return allErr
}

// FormatCoursesMessage 将新增课程列表格式化为推送文本。
func FormatCoursesMessage(courses []jwxt.CourseInfo) string {
	if len(courses) == 0 {
		return "【选课监控】本轮没有新增课程。"
	}

	var b strings.Builder
	b.WriteString("【选课监控】发现新课程！\n")
	for i, course := range courses {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("━━━━━━━━━━━━━━━━\n")
		b.WriteString(fmt.Sprintf("课程名称：%s\n", nonEmpty(course.Kcmc, "未知")))
		b.WriteString(fmt.Sprintf("课程号：%s\n", nonEmpty(course.Kch, "未知")))
		b.WriteString(fmt.Sprintf("授课教师：%s\n", nonEmpty(course.Skls, "未知")))
		b.WriteString(fmt.Sprintf("上课时间：%s\n", nonEmpty(course.Sksj, "未知")))
		b.WriteString(fmt.Sprintf("上课地点：%s\n", nonEmpty(course.Skdd, "未知")))
		b.WriteString(fmt.Sprintf("剩余人数：%s\n", nonEmpty(course.Syrs, "未知")))
		b.WriteString(fmt.Sprintf("已选/排课：%d/%d\n", course.Xkrs, course.Pkrs))
		b.WriteString(fmt.Sprintf("开课单位：%s\n", nonEmpty(course.Dwmc, "未知")))
	}
	b.WriteString("━━━━━━━━━━━━━━━━")

	return b.String()
}

func nonEmpty(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
