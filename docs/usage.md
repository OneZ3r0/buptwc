# 使用指南

buptwc 根据 BUPT 强智课表安排 uCloud 通知查询，将新增的“互动课堂”通知发送到企业微信群机器人。课表只决定查询时间，不会按课程名称过滤通知。

当前提供 Linux 单账号 CLI。程序需要有效的 uCloud 会话、强智导出的课表，以及企业微信群机器人 Webhook。真实 CAS 登录和群消息发送仍需在部署环境验证，自动化测试使用本地模拟服务。

## 安装

使用 Go 1.25 或更新版本安装：

```sh
go install github.com/OneZ3r0/buptwc/cmd/buptwc@latest
```

二进制写入 `GOBIN`，未设置时通常为 `~/go/bin`。请将安装目录加入 `PATH`。开发中的版本也可在仓库根目录执行 `go install ./cmd/buptwc` 安装。

当前仓库尚未提供自动发布的预编译下载包。自行构建或收到对应架构的 Linux 二进制后，可以安装到用户目录：

```sh
install -Dm755 ./buptwc "$HOME/.local/bin/buptwc"
```

此时将 `~/.local/bin` 加入 `PATH` 即可；运行二进制不需要 Go 环境。

## 初始化与配置

```sh
buptwc init
```

默认创建 `~/.config/buptwc/config.toml`；设置了绝对路径 `XDG_CONFIG_HOME` 时，改用 `$XDG_CONFIG_HOME/buptwc/config.toml`。模板随二进制分发，初始化不依赖当前目录或源码仓库。配置文件权限为 `0600`，新建配置目录权限为 `0700`；已有配置不会覆盖。

初始化只创建 TOML，不登录、不创建数据库、不写入 Webhook。第一教学周默认为空，需要按实际校历填写。自定义配置路径时使用 `buptwc --config /path/to/config.toml init`，后续命令也传入同一路径。

编辑 `config.toml`，至少核对以下内容：

| 配置 | 含义 |
| --- | --- |
| `semester.start_monday` | 第一教学周的周一，格式为 `YYYY-MM-DD` |
| `semester.weeks` | 学期总周数 |
| `periods` | 从 1 连续编号的节次及起止时间 |
| `state_dir` | 会话和数据库的存放目录 |

初始化不会推断学期日期，提供的节次时间取自仓库课表示例。请按自己的校历和作息核对。时区默认 `Asia/Shanghai`。

默认在整段课程开始前 10 分钟至结束后 5 分钟查询通知。`window.mode = "period"` 可改为按每小节生成窗口；重叠或相接的窗口会合并。

分页、超时、重试和平台请求头已有默认值。需要调整时，取消 [配置示例](../config.example.toml) 中相应配置节及字段的注释；配置中未知的字段名会被拒绝。

## 登录、导入与检查

```sh
buptwc login
buptwc import /path/to/timetable.xls
buptwc courses
buptwc plan --days 14
buptwc check
```

依次确认：登录成功；课程的周次、星期和节次正确；预览窗口符合课表；`check` 能完整读取通知列表。`plan` 默认从今天开始，也可以指定 `--from YYYY-MM-DD`。

`check` 不建立通知基线，也不发送提醒。查询收件人默认使用登录响应中的 `user_id`；若与平台实际请求不符，在 `[ucloud]` 中设置 `account_id`。角色和租户请求头分别由 `identity`、`tenant_id` 配置。

交互登录隐藏密码输入，不保存密码。脚本可使用 `login --username USER --password-stdin`。如果 CAS 要求验证码，当前密码登录流程无法继续；`login --ticket-stdin` 接受服务地址为 `https://ucloud.bupt.edu.cn` 的、尚未兑换的 CAS Ticket。程序不负责浏览器验证码操作，Ticket 只能使用一次。

## 发送提醒

将 Webhook 放入当前进程的 `BUPTWC_WEBHOOK` 环境变量，再执行：

```sh
buptwc notify test
buptwc run
```

`notify test` 会向配置的群发送一条真实测试消息。`run` 在监控窗口内查询平台，窗口外等待；已经进入发送队列的任务在窗口外仍会继续处理。

**首次完整扫描只记录已有通知，不发送其中的任何消息。** 这次扫描发生在第一个有效监控窗口内，单纯提前启动进程不会在窗口外建立基线。第一次扫描中已经出现的互动课堂通知也不会推送。之后的基线和去重记录保存在数据库中，重启不会重新建立基线。

成功扫描后默认等待 10 秒再查询。请求耗时、重试退避和发送队列处理会延长实际间隔，程序不保证每隔 10 秒完成一次扫描。监控时，一轮通知读取共享 `poll.timeout`（默认 15 秒），一批发送共享 `notify.timeout`（默认 10 秒）；分页较慢导致整轮超时时，可调整 `poll.timeout`。

运行期间可在另一个终端使用：

```sh
buptwc status
buptwc courses
buptwc plan
```

修改配置、重新登录、导入课表或执行其他需要独占状态目录的命令前，先停止监控进程，完成操作后再启动。

## 命令与路径

```text
buptwc [--config FILE] [--state-dir DIR] <command>
```

全局选项必须放在命令前，例如：

```sh
buptwc --config /path/to/config.toml plan --days 14
```

| 命令 | 用途 |
| --- | --- |
| `init` | 创建配置文件，拒绝覆盖已有文件 |
| `login` | 登录并保存会话 |
| `logout` | 删除会话，保留课表、基线和发送记录 |
| `import FILE` | 校验并替换课表；失败时保留原课表 |
| `courses` | 显示已导入的排课 |
| `plan` | 预览监控窗口 |
| `check` | 检查会话和完整通知分页 |
| `notify test` | 发送测试消息 |
| `status` | 查看会话、课表及发送任务状态 |
| `run` | 启动监控并处理发送队列 |

默认配置路径与当前工作目录无关，不会自动读取当前目录下的 `config.toml`。已有本地配置可以继续通过 `--config ./config.toml` 使用。

配置中的相对 `state_dir` 相对于配置文件目录解析；命令行 `--state-dir` 相对于当前工作目录解析。默认状态位于配置目录下的 `state/`，会话和数据库与 TOML 分开存放。`init` 不接受 `--state-dir`，需要自定义时编辑生成配置中的 `state_dir`。所有命令应使用同一套配置和状态目录，否则会看到不同的会话和课表。

## 支持的课表格式

支持 OLE/BIFF `.xls`，以及扩展名为 `.xls` 的 HTML 表格，包括声明 GBK 编码的 HTML。暂不支持 `.xlsx`。文件必须只包含一张可识别的课表，表头须有星期一至星期日，也可以使用“周一”等写法。

典型课程块：

```text
计算机网络
张老师
3-8,10-18[周]
教学楼 101
[03-04]节
```

支持的表示方式包括：

| 字段 | 示例 |
| --- | --- |
| 连续、离散周次 | `1-16(周)`、`1,3,5-8周`、`3-8,10-18[周]` |
| 单双周 | `1-16(单周)`、`2-16周(双)` |
| 节次范围 | `[01-02节]`、`[01-02]节` |
| 连续节次枚举 | `[06-07-08]节` |

名称和教师各占一行。名称后独占一行的数字编号会保留为名称后缀，例如 `运动表现提升训练(112)`。地点可放在周次前或后；同一单元格内多门课程的字段顺序必须一致。节次也可与周次同行，或取自行标题。

重复出现在多个节次行的同一排课会去重，独占一行的“备注：”不会作为课程导入。无法识别的课程单元格会报告行列位置；损坏文件或其他格式错误会终止导入。其他导出布局未必兼容，成功导入后仍应核对 `courses` 和 `plan`。

调停课通过 `[[adjustments]]` 配置，支持 `cancel`、`move`、`add`，示例见配置文件。规则按声明顺序执行；取消或移动规则未匹配到课程时会报错。`course` 应使用 `courses` 输出的完整名称。

## 使用 systemd 常驻运行

随附服务使用 `~/.local/bin/buptwc` 和 `~/.config/buptwc/config.toml` 的固定路径。以下示例从源码构建后安装；已有配置时跳过初始化。使用其他配置目录时，需要相应调整服务文件中的 `ExecStart` 和 `EnvironmentFile`：

```sh
make build
install -Dm755 bin/buptwc "$HOME/.local/bin/buptwc"
install -d -m700 "$HOME/.config/buptwc"
if [ ! -e "$HOME/.config/buptwc/config.toml" ]; then
    "$HOME/.local/bin/buptwc" --config "$HOME/.config/buptwc/config.toml" init
fi
install -Dm644 deploy/buptwc.service "$HOME/.config/systemd/user/buptwc.service"
umask 077
touch "$HOME/.config/buptwc/secrets.env"
chmod 600 "$HOME/.config/buptwc/secrets.env"
```

编辑该目录下的 `config.toml`，将 `state_dir` 设为选定的绝对路径；编辑 `secrets.env`，加入：

```text
BUPTWC_WEBHOOK=https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=你的密钥
```

使用这个配置路径重新完成登录和课表检查。例如：

```sh
"$HOME/.local/bin/buptwc" --config "$HOME/.config/buptwc/config.toml" login
"$HOME/.local/bin/buptwc" --config "$HOME/.config/buptwc/config.toml" import /path/to/timetable.xls
"$HOME/.local/bin/buptwc" --config "$HOME/.config/buptwc/config.toml" plan
"$HOME/.local/bin/buptwc" --config "$HOME/.config/buptwc/config.toml" check
```

`secrets.env` 由 systemd 读取，普通终端不会自动加载它。在终端执行 `notify test` 时仍需设置 Webhook 环境变量。

```sh
systemctl --user daemon-reload
systemctl --user enable --now buptwc
journalctl --user -u buptwc -f
```

更新配置或会话时先执行 `systemctl --user stop buptwc`，完成后再 `start`。用户服务能否在退出登录后继续运行取决于系统设置；程序不会自动配置 linger。

## 故障处理

| 现象 | 检查或处理 |
| --- | --- |
| 没有监控窗口 | 用 `plan --from` 检查学期日期、周次、节次和调停课规则 |
| 首次运行没有提醒 | 检查是否刚建立基线，以及是否处于监控窗口 |
| 状态目录被占用 | 停止使用相同目录的监控进程；不要删除锁文件来绕过检查 |
| 会话失效 | 等待当前账号队列处理结束，重新登录，再启动监控 |
| 企业微信发送失败 | 查看日志中的 HTTP 状态或 `errcode`，核对 Webhook 和机器人状态 |
| 任务已过期或达到重试上限 | 程序不会继续自动发送；`status` 可查看任务统计 |

退出码 `2` 用于已分类的配置、会话或缺少课表等错误，随附 systemd 服务不会对此自动重启；其他返回错误使用退出码 `1`。日志输出到标准错误，命令结果输出到标准输出。

投递可能重复：企业微信已收到消息但响应丢失，或程序在记录成功前退出时，重试可能再次发送。具体一致性约束见 [设计说明](buptwc-design.md)。修改代码和运行测试见 [开发指南](development.md)。
