# 开发指南

本指南面向修改和测试 buptwc 的开发者。安装、配置与部署见 [使用指南](usage.md)，数据流和一致性约束见 [设计说明](buptwc-design.md)。

## 环境与常用命令

开发目标为 Linux，需要 Go 1.25 或更新版本。构建使用纯 Go SQLite 驱动，发布二进制不依赖 C 编译器；`-race` 测试需要支持的 C 工具链。时区数据已嵌入 CLI 二进制。

在仓库根目录执行：

```sh
make build             # 输出 bin/buptwc
make test              # 全部测试
make test-unit         # internal/ 和 cmd/ 包内测试
make test-integration  # tests/ 跨模块测试
make check             # go vet 与全部 race 测试
```

修改代码后先运行相关包的测试，再运行 `make check` 和 `make build`。例如修改课表解析：

```sh
go test ./internal/schedule/...
go test ./tests/... -run 'TestCLIExampleImport|TestExampleCourseSemantics'
```

[GitHub Actions](../.github/workflows/ci.yml) 检查 Go 格式，运行 `make check` 并构建 Linux 二进制。

## 代码导航

| 位置 | 职责 |
| --- | --- |
| `cmd/buptwc/` | 命令参数、依赖组装、终端输出和退出码 |
| `internal/config/` | 用户配置路径、初始化模板、TOML 解码及配置校验 |
| `internal/schedule/` | 文件解码、课程解析、日期展开和窗口合并 |
| `internal/auth/` | CAS 登录、Ticket 交换及会话文件 |
| `internal/ucloud/` | 通知请求、分页完整性校验及字段匹配 |
| `internal/monitor/` | 查询、发送、退避和唤醒调度 |
| `internal/store/` | SQLite 课表、通知基线、去重及发送队列 |
| `internal/notify/` | 企业微信文本消息和业务响应检查 |
| `internal/state/` | 状态目录、私有文件及进程锁 |
| `internal/netutil/` | HTTP 客户端、响应读取、ID 解码和退避计算 |

课表相关改动通常从 `schedule/import.go` 开始；BIFF 和 HTML 解码位于 `schedule/decode.go`。命令行入口为 `execute`，只负责公共参数及命令分派；`cmd/buptwc/commands.go` 中的命令函数负责各自的校验和资源生命周期。登录只检查请求超时与显示时区，不依赖分页配置；测试发送不依赖重试和 TTL 配置。长期运行入口为 `monitor.Runner.Run`。

## 测试约定

包内测试检查模块行为，`tests/` 检查跨模块流程。测试使用临时状态目录和本地 HTTP 服务，不需要学校账号或真实 Webhook。

| 测试 | 验证内容 |
| --- | --- |
| `internal/schedule/*_test.go` | 周次、节次、课程块、导入格式、调停课及窗口边界 |
| `internal/store/store_test.go` | 事务回滚、去重、账号隔离、重启恢复和并行读取 |
| `internal/monitor/monitor_test.go` | 运行循环、窗口、退避、取消及会话失效处理 |
| `internal/auth/auth_test.go` | 模拟 CAS 登录、Ticket 交换及失败响应 |
| `tests/init_test.go` | 通过 `go install` 安装，在仓库外使用默认路径初始化、导入和查询 |
| `tests/cli_test.go` | 构建 CLI，导入仓库 XLS，查看课程、预览窗口及失败导入保留旧课表 |
| `tests/monitor_test.go` | 平台分页、持久化队列、企业微信业务失败与恢复 |

`internal/config/initial.toml` 是嵌入二进制的初始化模板，不包含凭据或预设学期日期。修改模板后检查初始化、权限和重复执行测试；仓库的 `config.example.toml` 则提供包含高级选项的完整示例。

集成测试明确引用 `example/` 中的课表，使用 `tests/testdata/config.toml` 固定日期和节次。当前样本预期为 16 条排课和 133 次课程；这是测试数据的断言，不是对所有课表的限制。增加示例文件不应改变已有测试输入。

修改行为时，测试应描述触发条件和可观察结果。涉及调度顺序时使用虚拟时间或显式同步；不要依靠短暂的真实等待证明事件先后。测试准备步骤也应检查错误，并保证失败时能释放资源。

## 修改时需要保留的约束

- 导入失败不能替换旧课表。增加布局支持时，同时保留拒绝歧义输入的测试。
- 分页未完整成功时，不能提交任何已见记录或发送任务。
- 新通知的去重记录与发送任务必须在同一事务中提交。
- 同一状态目录的写入和平台调用需要独占进程锁；已有任务按账号隔离。
- 网络测试必须能使用本地服务，错误信息不应带出 Token、Ticket 或 Webhook 密钥。

这些约束由具体函数和事务维护，不需要额外的通用框架。变更数据库结构时更新 `user_version` 并提供升级路径；当前已存在的版本 1 数据库在打开时跳过建表事务。

## 联调范围

自动化测试验证本地逻辑和模拟协议，不能证明学校平台与企业微信的实际行为。部署前仍需按使用指南核对 CAS 登录、收件人 ID、角色请求头、通知时间和群消息投递。

提交问题时附上程序命令、配置中的非敏感部分、错误信息及最小复现输入。课表样本应去除不需要的个人信息；不要提交会话文件、密码或 Webhook。
