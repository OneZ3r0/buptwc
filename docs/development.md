# 开发与运行

当前实现为 Linux 单账号 CLI，需要 Go 1.25 或更新版本。SQLite 使用纯 Go 驱动，构建二进制不需要 C 编译器。时区数据已嵌入二进制。

## 本地启动

```sh
make build
cp config.example.toml config.toml
# 修改第一教学周周一、节次时间和学期周数。
./bin/buptwc login
./bin/buptwc import /path/to/timetable.xls
./bin/buptwc courses
./bin/buptwc plan --days 14
./bin/buptwc check
```

`check` 完整读取通知列表，输出总数和互动课堂数量，不建立基线、不修改已见记录、不发送消息。默认 `newsCopyPersonId` 使用登录响应的 `user_id`；如实际平台使用不同收件人 ID，可通过 `ucloud.account_id` 覆盖。`identity` 和 `tenant_id` 也可配置。

将企业微信群机器人 Webhook 放入 `BUPTWC_WEBHOOK` 环境变量，再执行：

```sh
./bin/buptwc notify test
./bin/buptwc run
```

`notify test` 会真正发送群消息。`run` 首次在监控窗口内完整扫描时只建立基线，因此应在需要提醒的第一堂课之前启动并完成一次基线扫描。已有基线会跨重启保留。`status` 可以在监控进程运行时查看状态；`courses`、`plan` 也可并行读取。

全局选项须放在命令前，例如 `buptwc --config /path/config.toml plan --days 14`。相对 `state_dir` 相对于配置文件所在目录解析；`--state-dir` 则相对于当前工作目录。修改配置、重新登录或导入课表前须停止运行进程，完成后重启。

## 导入格式

支持 OLE/BIFF `.xls`，以及强智导出中以 `.xls` 命名的 HTML 表格（包括声明 GBK 编码的文件）。表头须包含星期一至星期日，可写成“周一”等形式；HTML 合并单元格会保持列位置。一个文件须只包含一张可识别的课表。

典型课程单元格如下，多门课程按同样顺序连续排列；空行或分隔横线可省略：

```text
计算机网络
张老师
1-16(周)[01-02节]
教学楼 101

离散数学
李老师
3,5,7-11周(单)[01-02节]
教学楼 202
```

支持离散周次、范围、单双周和连续节次，例如 `1-16(单周)`、`2-16周(双)`、`1,3,5-8周`。节次也可独占下一行或取自左侧含“节”的行标题。课程名称和教师各占一行。无法明确识别的内容会报出行列，整次导入失败，旧课表保持不变；不会跳过失败单元格。真实强智导出可能有其他布局，接入时须用实际课表核对 `courses` 和 `plan` 输出。

课表决定轮询时段，不匹配平台课程 ID。`course` 模式包含整段课程中间的课间休息；`period` 模式分别生成每小节窗口。窗口偏移以非负时长配置，重叠或相接的窗口合并。窗口按 `[开始, 结束)` 处理。调停课配置按声明顺序执行，未匹配的停课或调课会报错，避免拼错课程名称后静默失效。

## 状态与失败恢复

- 状态目录权限为 `0700`，会话文件为 `0600`，密码不落盘。登录支持隐藏密码输入，自动化可用 `login --username USER --password-stdin`，不要把密码作为命令行参数。
- 遇到 CAS 验证码时，交互密码登录会提示失败。可在浏览器完成认证，取得服务为 `https://ucloud.bupt.edu.cn` 的尚未兑换 Ticket，通过 `login --ticket-stdin` 传入；Ticket 为一次性凭据。
- `logout` 只删除会话。课表、基线、已见通知和发送任务保留。去重和发送任务以收件人账号 ID 隔离，切换账号不会发送旧账号任务。
- 每次扫描完整读取所有分页，不因已见 ID 提前终止。HTTP、业务状态、结构、总量、页数或重复 ID 校验失败时，整次扫描不提交。列表在分页中变化时退避重试；未验证服务端提供快照一致性，在总量不变时发生替换仍有竞态，后续扫描可能补获。
- 已见通知和发送任务在同一 SQLite 事务内写入。相同 ID 内容更新不重复提醒。重启后恢复未完成任务，发送重试不受课程窗口限制。
- 仅匹配 `互动课堂` / `chat` / `TO_CLASS` 三个字段；`isRead` 不参与去重。消息使用文本，`TO_CLASS` 不拼成链接。
- 提醒时间优先取 `newsCopyTime`，其次 `createTime`，支持 RFC3339 和本地 `YYYY-MM-DD HH:MM[:SS]`。两者都无法解析时使用首次观察时间；未来时间截到观察时间。按该时间加 `notify.ttl` 得到过期时间。这是可运行的首版策略，平台时间语义仍待验证。
- 企业微信返回 `errcode=0` 才视为成功。失败采用有上限的指数退避，总尝试次数由 `max_attempts` 限制；到期或下次重试将越过有效期时停止。进程退出时不丢弃任务。Webhook 响应丢失、或发送成功后事务提交前进程崩溃，仍可能重复投递。
- 网络错误日志不包含请求 URL、Token、Webhook 密钥或服务端原始响应。状态目录使用内核文件锁，进程退出后自动释放。日志写标准错误，业务命令结果写标准输出。
- 会话失效时停止平台轮询，先继续处理当前账号发送队列；队列处理完后退出，需重新登录并重启。Refresh Token 自动续期暂未启用。

## systemd 用户服务

```sh
install -Dm755 bin/buptwc "$HOME/.local/bin/buptwc"
install -Dm600 config.example.toml "$HOME/.config/buptwc/config.toml"
install -Dm644 deploy/buptwc.service "$HOME/.config/systemd/user/buptwc.service"
```

修改配置，将 `state_dir` 设为自己的绝对路径，并在 `~/.config/buptwc/secrets.env` 中设置 `BUPTWC_WEBHOOK`（权限 `0600`）。用同一配置路径完成 `login`、`import`、`plan`、`check` 和 `notify test` 后执行：

```sh
systemctl --user daemon-reload
systemctl --user enable --now buptwc
journalctl --user -u buptwc -f
```

用户级服务的后台存活取决于系统的用户会话设置。程序不会自动安装、启用服务或配置 linger。

## 验证与待联调

```sh
make test
make check
make build
```

测试使用虚构课表和本地 HTTP 服务，覆盖导入、窗口、分页、基线、事务回滚、重启、发送业务失败和恢复。真实账号 CAS、验证码流程、角色请求头、收件人 ID 映射、通知时间语义和企业微信群发送需在部署环境联调。参考协议来自 [bupt-ucloud-tool](https://github.com/OneZ3r0/bupt-ucloud-tool) 和 [bupt-auth](https://github.com/byrdocs/bupt-auth)。
