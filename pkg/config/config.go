package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

const (
	DefaultPollInterval = 2
)

// Config 保存监控程序的全部运行配置。
type Config struct {
	Username     string
	Password     string
	OneBotURL    string
	OneBotToken  string
	GroupList    []string
	CourseList   []string
	PollInterval int
}

// Load 从环境变量和 .env 文件加载配置并完成校验。
func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		Username:     strings.TrimSpace(os.Getenv("QFNU_USERNAME")),
		Password:     os.Getenv("QFNU_PASSWORD"),
		OneBotURL:    strings.TrimRight(strings.TrimSpace(os.Getenv("ONEBOT_URL")), "/"),
		OneBotToken:  strings.TrimSpace(os.Getenv("ONEBOT_TOKEN")),
		GroupList:    splitAndTrim(os.Getenv("GROUP_LIST")),
		CourseList:   splitAndTrim(os.Getenv("COURSE_LIST")),
		PollInterval: DefaultPollInterval,
	}

	if raw := strings.TrimSpace(os.Getenv("POLL_INTERVAL")); raw != "" {
		pollInterval, err := strconv.Atoi(raw)
		if err != nil || pollInterval <= 0 {
			cfg.PollInterval = DefaultPollInterval
		} else {
			cfg.PollInterval = pollInterval
		}
	}

	var missing []string
	if cfg.Username == "" {
		missing = append(missing, "QFNU_USERNAME")
	}
	if cfg.Password == "" {
		missing = append(missing, "QFNU_PASSWORD")
	}
	if cfg.OneBotURL == "" {
		missing = append(missing, "ONEBOT_URL")
	}
	if len(cfg.GroupList) == 0 {
		missing = append(missing, "GROUP_LIST")
	}
	if len(cfg.CourseList) == 0 {
		missing = append(missing, "COURSE_LIST")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("缺少必填配置项: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

func splitAndTrim(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	items := strings.Split(raw, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		value := strings.TrimSpace(item)
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}
