# easy-qfnu-xk-monitor

曲阜师范大学（QFNU）选课监控工具。  
程序通过 CAS 登录教务系统，自动获取选课轮次并轮询课程搜索接口，发现新增课程后通过 OneBot HTTP 推送到 QQ 群。

## 功能特性

- CAS 登录与会话维持
- 选课轮次 DOM 解析（`#tbKxkc`）
- 五个选课模块统一搜索与去重
- 课程新增检测与首轮基线策略
- 快照持久化（`data/last_result.json`）
- 会话失效自动重登与重试
- OneBot 群消息广播推送

## 项目结构

```text
easy-qfnu-xk-monitor/
├── main.go
├── .env.example
├── docs/
├── cmd/demo/
└── pkg/
    ├── auth/      # CAS 密码加密
    ├── cas/       # CAS 登录
    ├── config/    # 配置加载与校验
    ├── jwxt/      # 轮次获取与课程搜索
    ├── monitor/   # 轮询监控与快照管理
    └── notify/    # OneBot 推送
```

## 环境要求

- Go 1.25+
- 可访问 QFNU 教务系统
- 已运行的 OneBot HTTP 服务

## 快速开始

### 1. 安装依赖

```bash
git clone https://github.com/W1ndys/easy-qfnu-xk-monitor.git
cd easy-qfnu-xk-monitor
go mod download
```

### 2. 配置环境变量

复制并编辑 `.env`：

```bash
cp .env.example .env
```

`.env` 关键项：

- `QFNU_USERNAME`: 学号/工号
- `QFNU_PASSWORD`: 登录密码
- `ONEBOT_URL`: OneBot HTTP 地址（例如 `http://127.0.0.1:3000`）
- `ONEBOT_TOKEN`: OneBot Token（可选）
- `GROUP_LIST`: 推送群号，逗号分隔
- `COURSE_LIST`: 监控课程号，逗号分隔
- `POLL_INTERVAL`: 轮询间隔秒数（默认 2）

### 3. 运行主程序

```bash
go run .
```

可选参数：

- `-t`: 请求超时（默认 `30s`）

## 编译

```bash
go build -v .
```

可执行文件示例：

- Windows: `go build -o easy-qfnu-xk-monitor.exe .`
- Linux/macOS: `go build -o easy-qfnu-xk-monitor .`

## Demo（仅登录示例）

`cmd/demo` 仍可用于 CAS 登录连通性测试：

```bash
go run ./cmd/demo -u <学号> -p <密码>
```

## 文档

- `docs/qfnu-cas-login-api.md`: CAS 登录协议说明
- `docs/选课API.md`: 五模块搜索与选课接口
- `docs/获取选课轮次.md`: 轮次页面解析说明
- `docs/选课监控开发方案.md`: 整体设计与实现规划

## 注意事项

- 本项目会高频请求教务系统，请合理设置 `POLL_INTERVAL`。
- `data/` 目录为运行时数据目录，已在 `.gitignore` 中忽略。
- 仅用于学习与个人自动化场景，请遵守学校与平台使用规范。
