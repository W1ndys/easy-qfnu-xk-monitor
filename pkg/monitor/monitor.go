package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/W1ndys/qfnu-cas-go/pkg/cas"
	"github.com/W1ndys/qfnu-cas-go/pkg/config"
	"github.com/W1ndys/qfnu-cas-go/pkg/jwxt"
	"github.com/W1ndys/qfnu-cas-go/pkg/notify"
)

const (
	defaultSnapshotPath = "data/last_result.json"
)

// Monitor 负责轮询课程、检测新增并推送消息。
type Monitor struct {
	casClient    *cas.Client
	client       *http.Client
	config       *config.Config
	notifier     *notify.Notifier
	lastResult   map[string]jwxt.CourseInfo
	hasBaseline  bool
	snapshotPath string
}

// New 创建监控器，并尝试加载历史快照。
func New(casClient *cas.Client, cfg *config.Config, notifier *notify.Notifier) (*Monitor, error) {
	if casClient == nil {
		return nil, fmt.Errorf("casClient 不能为空")
	}
	if cfg == nil {
		return nil, fmt.Errorf("config 不能为空")
	}
	if notifier == nil {
		return nil, fmt.Errorf("notifier 不能为空")
	}

	m := &Monitor{
		casClient:    casClient,
		client:       casClient.GetClient(),
		config:       cfg,
		notifier:     notifier,
		lastResult:   make(map[string]jwxt.CourseInfo),
		snapshotPath: defaultSnapshotPath,
	}

	snapshot, err := m.loadSnapshot()
	if err == nil {
		m.lastResult = snapshot
		m.hasBaseline = true
		log.Printf("[INFO] 已加载历史快照: %s, 条目=%d", m.snapshotPath, len(snapshot))
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Printf("[WARN] 历史快照加载失败，将使用首轮基线模式: %v", err)
	}

	return m, nil
}

// Run 启动监控循环，直到 context 被取消。
func (m *Monitor) Run(ctx context.Context) error {
	log.Printf("[INFO] 监控启动: 轮询间隔=%ds, 课程关键词=%d", m.config.PollInterval, len(m.config.CourseList))
	m.runRound(ctx)

	ticker := time.NewTicker(time.Duration(m.config.PollInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[INFO] 监控停止: %v", ctx.Err())
			return nil
		case <-ticker.C:
			m.runRound(ctx)
		}
	}
}

func (m *Monitor) runRound(ctx context.Context) {
	startedAt := time.Now()

	current, err := m.queryCurrentCourses(ctx)
	if err != nil {
		if jwxt.IsSessionExpired(err) {
			log.Printf("[WARN] 检测到会话失效，准备重登: %v", err)
			if reloginErr := m.reloginWithRetry(ctx); reloginErr != nil {
				log.Printf("[ERROR] 会话恢复失败: %v", reloginErr)
			}
			return
		}
		log.Printf("[ERROR] 本轮查询失败，已跳过: %v", err)
		return
	}

	added := m.diffAdded(current)
	if !m.hasBaseline {
		m.lastResult = current
		m.hasBaseline = true
		if err := m.saveSnapshot(current); err != nil {
			log.Printf("[WARN] 保存首轮快照失败: %v", err)
		}
		log.Printf("[INFO] 首轮基线已建立: 当前课程=%d, 耗时=%s", len(current), time.Since(startedAt))
		return
	}

	if len(added) > 0 {
		message := notify.FormatCoursesMessage(added)
		if err := m.notifier.BroadcastMessage(message); err != nil {
			log.Printf("[ERROR] 新增课程推送失败: %v", err)
		} else {
			log.Printf("[INFO] 已推送新增课程: %d 条", len(added))
		}
	}

	m.lastResult = current
	if err := m.saveSnapshot(current); err != nil {
		log.Printf("[WARN] 保存快照失败: %v", err)
	}
	log.Printf("[INFO] 本轮完成: 总课程=%d, 新增=%d, 耗时=%s", len(current), len(added), time.Since(startedAt))
}

func (m *Monitor) queryCurrentCourses(ctx context.Context) (map[string]jwxt.CourseInfo, error) {
	current := make(map[string]jwxt.CourseInfo)
	for _, keyword := range m.config.CourseList {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		courses, err := jwxt.SearchAllModules(ctx, m.client, keyword)
		if err != nil {
			return nil, fmt.Errorf("课程[%s]搜索失败: %w", keyword, err)
		}
		for _, course := range courses {
			key := course.UniqueKey()
			if key == "_" {
				continue
			}
			current[key] = course
		}
	}
	return current, nil
}

func (m *Monitor) diffAdded(current map[string]jwxt.CourseInfo) []jwxt.CourseInfo {
	added := make([]jwxt.CourseInfo, 0)
	for key, course := range current {
		if _, exists := m.lastResult[key]; !exists {
			added = append(added, course)
		}
	}

	sort.Slice(added, func(i, j int) bool {
		return added[i].UniqueKey() < added[j].UniqueKey()
	})
	return added
}

func (m *Monitor) reloginWithRetry(ctx context.Context) error {
	backoff := 2 * time.Second
	for attempt := 1; ; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		log.Printf("[INFO] 会话恢复第 %d 次尝试", attempt)
		if err := m.casClient.Login(ctx, m.config.Username, m.config.Password); err != nil {
			log.Printf("[ERROR] 重新登录失败: %v", err)
		} else {
			roundID, err := jwxt.GetSelectionRoundID(ctx, m.casClient.GetClient())
			if err != nil {
				log.Printf("[ERROR] 重新获取轮次失败: %v", err)
			} else if err := jwxt.EnterSelectionRound(ctx, m.casClient.GetClient(), roundID); err != nil {
				log.Printf("[ERROR] 重新进入轮次失败: %v", err)
			} else {
				m.client = m.casClient.GetClient()
				log.Printf("[INFO] 会话恢复成功，已进入轮次: %s", roundID)
				return nil
			}
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}

		if backoff < time.Minute {
			backoff *= 2
			if backoff > time.Minute {
				backoff = time.Minute
			}
		}
	}
}

func (m *Monitor) saveSnapshot(data map[string]jwxt.CourseInfo) error {
	dir := filepath.Dir(m.snapshotPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建快照目录失败: %w", err)
	}

	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化快照失败: %w", err)
	}

	tmpPath := m.snapshotPath + ".tmp"
	if err := os.WriteFile(tmpPath, content, 0o644); err != nil {
		return fmt.Errorf("写入临时快照失败: %w", err)
	}

	if err := os.Rename(tmpPath, m.snapshotPath); err != nil {
		_ = os.Remove(m.snapshotPath)
		if err2 := os.Rename(tmpPath, m.snapshotPath); err2 != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("替换快照文件失败: %w", err2)
		}
	}

	return nil
}

func (m *Monitor) loadSnapshot() (map[string]jwxt.CourseInfo, error) {
	content, err := os.ReadFile(m.snapshotPath)
	if err != nil {
		return nil, err
	}
	if len(content) == 0 {
		return map[string]jwxt.CourseInfo{}, nil
	}

	var snapshot map[string]jwxt.CourseInfo
	if err := json.Unmarshal(content, &snapshot); err != nil {
		return nil, fmt.Errorf("解析快照失败: %w", err)
	}
	if snapshot == nil {
		snapshot = map[string]jwxt.CourseInfo{}
	}
	return snapshot, nil
}
