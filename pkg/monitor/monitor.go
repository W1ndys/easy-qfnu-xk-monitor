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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/W1ndys/easy-qfnu-xk-monitor/pkg/cas"
	"github.com/W1ndys/easy-qfnu-xk-monitor/pkg/config"
	"github.com/W1ndys/easy-qfnu-xk-monitor/pkg/jwxt"
	"github.com/W1ndys/easy-qfnu-xk-monitor/pkg/notify"
)

const (
	defaultSnapshotPath = "data/last_result.json"
)

var remainingSeatNumberPattern = regexp.MustCompile(`-?\d+`)

// Monitor 负责轮询课程、检测余量增加并推送消息。
type Monitor struct {
	casClient    *cas.Client
	client       *http.Client
	config       *config.Config
	notifier     *notify.Notifier
	ocrClient    cas.OCRClient
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

	// 创建 OCR 客户端
	ocrClient := cas.NewDefaultOCRClient(cfg.OCRApiURL)

	m := &Monitor{
		casClient:    casClient,
		client:       casClient.GetClient(),
		config:       cfg,
		notifier:     notifier,
		ocrClient:    ocrClient,
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

// Run 执行单轮监控并返回。
func (m *Monitor) Run(ctx context.Context) error {
	select {
	case <-ctx.Done():
		log.Printf("[INFO] 监控取消: %v", ctx.Err())
		return nil
	default:
	}

	log.Printf("[INFO] 监控启动: 单次执行模式, 课程关键词=%d", len(m.config.CourseList))
	m.runRound(ctx)
	log.Printf("[INFO] 监控结束: 单次执行完成")
	return nil
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

	increased := m.diffRemainingIncreased(current)
	if !m.hasBaseline {
		m.lastResult = current
		m.hasBaseline = true
		if err := m.saveSnapshot(current); err != nil {
			log.Printf("[WARN] 保存首轮快照失败: %v", err)
		}
		log.Printf("[INFO] 首轮基线已建立: 当前课程=%d, 耗时=%s", len(current), time.Since(startedAt))
		return
	}

	if len(increased) > 0 {
		message := notify.FormatCoursesMessage(increased)
		if err := m.notifier.BroadcastMessage(message); err != nil {
			log.Printf("[ERROR] 余量增加课程推送失败: %v", err)
		} else {
			log.Printf("[INFO] 已推送余量增加课程: %d 条", len(increased))
		}
	}

	m.lastResult = current
	if err := m.saveSnapshot(current); err != nil {
		log.Printf("[WARN] 保存快照失败: %v", err)
	}
	log.Printf("[INFO] 本轮完成: 总课程=%d, 余量增加=%d, 耗时=%s", len(current), len(increased), time.Since(startedAt))
}

func (m *Monitor) queryCurrentCourses(ctx context.Context) (map[string]jwxt.CourseInfo, error) {
	type result struct {
		courses []jwxt.CourseInfo
		err     error
		keyword string
	}

	resultCh := make(chan result, len(m.config.CourseList))
	var wg sync.WaitGroup

	// 启动并发搜索
	for _, keyword := range m.config.CourseList {
		wg.Add(1)
		go func(kw string) {
			defer wg.Done()

			courses, err := jwxt.SearchAllModules(ctx, m.client, kw)
			resultCh <- result{
				courses: courses,
				err:     err,
				keyword: kw,
			}
		}(keyword)
	}

	// 等待所有搜索完成后关闭 channel
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// 收集结果
	current := make(map[string]jwxt.CourseInfo)
	for res := range resultCh {
		if res.err != nil {
			return nil, fmt.Errorf("课程[%s]搜索失败: %w", res.keyword, res.err)
		}
		for _, course := range res.courses {
			key := course.UniqueKey()
			if key == "_" {
				continue
			}
			current[key] = course
		}
	}

	return current, nil
}

func (m *Monitor) diffRemainingIncreased(current map[string]jwxt.CourseInfo) []jwxt.CourseInfo {
	increased := make([]jwxt.CourseInfo, 0)
	for key, course := range current {
		lastCourse, exists := m.lastResult[key]
		if !exists {
			// 忽略“从无到有”的课程，仅关注同一课程余量变化。
			continue
		}

		currentRemaining, okCurrent := parseRemainingSeats(course.Syrs)
		lastRemaining, okLast := parseRemainingSeats(lastCourse.Syrs)
		if !okCurrent || !okLast {
			continue
		}
		if currentRemaining > lastRemaining {
			increased = append(increased, course)
		}
	}

	sort.Slice(increased, func(i, j int) bool {
		return increased[i].UniqueKey() < increased[j].UniqueKey()
	})
	return increased
}

func parseRemainingSeats(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}

	if n, err := strconv.Atoi(value); err == nil {
		return n, true
	}

	if strings.Contains(value, "满") || strings.Contains(value, "无") {
		return 0, true
	}

	raw := remainingSeatNumberPattern.FindString(value)
	if raw == "" {
		return 0, false
	}

	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return n, true
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
		if err := m.casClient.Login(ctx, m.config.Username, m.config.Password, m.ocrClient); err != nil {
			log.Printf("[ERROR] 重新登录失败: %v", err)
		} else {
			// 重新登录成功后保存 session
			if err := m.casClient.SaveSession(); err != nil {
				log.Printf("[WARN] 保存 session 失败: %v", err)
			}
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
