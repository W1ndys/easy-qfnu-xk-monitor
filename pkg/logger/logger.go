package logger

import (
	"io"
	stdlog "log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	"github.com/sirupsen/logrus"
)

const (
	defaultLogDir        = "logs"
	defaultMaxAgeDays    = 30
	defaultLogFilePrefix = "monitor"
)

// Init 初始化全局日志器，输出到控制台和按天切分的日志文件。
func Init() (func(), error) {
	logDir := strings.TrimSpace(os.Getenv("LOG_DIR"))
	if logDir == "" {
		logDir = defaultLogDir
	}

	maxAgeDays := defaultMaxAgeDays
	if raw := strings.TrimSpace(os.Getenv("LOG_MAX_AGE_DAYS")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			maxAgeDays = v
		}
	}

	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, err
	}

	writer, err := rotatelogs.New(
		filepath.Join(logDir, defaultLogFilePrefix+"-%Y-%m-%d.log"),
		rotatelogs.WithRotationTime(24*time.Hour),
		rotatelogs.WithMaxAge(time.Duration(maxAgeDays)*24*time.Hour),
		rotatelogs.WithClock(rotatelogs.Local),
	)
	if err != nil {
		return nil, err
	}

	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05.000",
	})
	logrus.SetOutput(io.MultiWriter(os.Stdout, writer))
	logrus.SetLevel(logrus.InfoLevel)

	// 兼容现有标准库 log 调用，统一转发到 logrus。
	stdWriter := logrus.StandardLogger().WriterLevel(logrus.InfoLevel)
	stdlog.SetFlags(0)
	stdlog.SetOutput(stdWriter)

	cleanup := func() {
		_ = stdWriter.Close()
		_ = writer.Close()
	}
	return cleanup, nil
}
