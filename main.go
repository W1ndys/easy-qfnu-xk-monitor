package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/W1ndys/qfnu-cas-go/pkg/cas"
	"github.com/W1ndys/qfnu-cas-go/pkg/config"
	"github.com/W1ndys/qfnu-cas-go/pkg/jwxt"
	"github.com/W1ndys/qfnu-cas-go/pkg/monitor"
	"github.com/W1ndys/qfnu-cas-go/pkg/notify"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[ERROR] 配置加载失败: %v", err)
	}

	timeout := flag.Duration("t", 30*time.Second, "请求超时时间")
	flag.Parse()

	log.Printf("[INFO] 启动配置: username=%s onebot=%s groups=%d courses=%d poll_interval=%ds",
		cfg.Username, cfg.OneBotURL, len(cfg.GroupList), len(cfg.CourseList), cfg.PollInterval)

	casClient, err := cas.NewClient(cas.WithTimeout(*timeout))
	if err != nil {
		log.Fatalf("[ERROR] 初始化 CAS 客户端失败: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("[INFO] 正在登录教务系统: %s", cfg.Username)
	startTime := time.Now()
	if err := casClient.Login(ctx, cfg.Username, cfg.Password); err != nil {
		log.Fatalf("[ERROR] 登录失败: %v", err)
	}
	duration := time.Since(startTime)
	log.Printf("[INFO] 登录成功, 耗时=%s", duration)

	roundID, err := jwxt.GetSelectionRoundID(ctx, casClient.GetClient())
	if err != nil {
		log.Fatalf("[ERROR] 获取选课轮次失败: %v", err)
	}
	if err := jwxt.EnterSelectionRound(ctx, casClient.GetClient(), roundID); err != nil {
		log.Fatalf("[ERROR] 进入选课轮次失败: %v", err)
	}
	log.Printf("[INFO] 已进入选课轮次: %s", roundID)

	notifier := notify.NewNotifier(
		cfg.OneBotURL,
		cfg.OneBotToken,
		cfg.GroupList,
		&http.Client{Timeout: 10 * time.Second},
	)

	worker, err := monitor.New(casClient, cfg, notifier)
	if err != nil {
		log.Fatalf("[ERROR] 创建监控器失败: %v", err)
	}

	if err := worker.Run(ctx); err != nil {
		log.Fatalf("[ERROR] 监控异常退出: %v", err)
	}
	log.Printf("[INFO] 程序已退出")
}
