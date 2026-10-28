# buptwc

导入 BUPT 强智课表，在上课时间附近轮询 uCloud，将新增“互动课堂”通知推送到企业微信群。

**技术栈：** Go、TOML、SQLite；单账号、单进程、CLI；Linux 二进制 + systemd。当前暂时不做 Web UI，ICS 后置

## 模块

| 模块 | 职责 |
| --- | --- |
| `auth` / `ucloud` | CAS 登录、会话管理、平台请求 |
| `schedule` | XLS 导入、课表展开、监控窗口 |
| `monitor` | 轮询、匹配、去重 |
| `notify` / `store` | 企业微信发送、SQLite 持久化 |

业务逻辑独立于 CLI；目录沿用本地约定，不引入 Redis、MQ、ORM。

## 课表与调度

- 导入强智 `.xls`，支持多课程单元格、离散周次、单双周、连续节次；校验成功后原子替换课表。
- 排课字段：课程名、教师、地点、周次、星期、起止节次。配置第一教学周周一、节次时间及调停课；时区默认 `Asia/Shanghai`。
- 默认整段课程窗口，也支持每小节窗口；课前/课后偏移可配置，重叠窗口合并。
- 窗口内默认每 `10s` 轮询，窗口外休眠；同账号请求串行。课表只控制轮询时间，不强行映射平台课程 ID。

## uCloud

登录参考 [bupt-ucloud-tool](https://github.com/OneZ3r0/bupt-ucloud-tool)：BUPT CAS 登录取得 Ticket，再向 `/ykt-basics/oauth/token` 提交表单 `grant_type=third`、`ticket`，取得会话。自动刷新需验证；会话不可恢复时提示重新登录，网络故障退避重试。

已提供的通知请求样本：

```http
POST https://apiucloud.bupt.edu.cn/ykt-basics/api/inform/news/list?newsCopyPersonId=<account_id>&current=1&size=10
Blade-Auth: <access_token>
Authorization: Basic <平台客户端凭据>
Tenant-Id: 000000
Identity: JS005:undefined
```

空请求体。列表为 `data.records`；分页字段为 `data.total/size/current/pages`。ID 按字符串处理，标题/内容取 `newsTitle/newsInfo`，时间字段为 `createTime/newsCopyTime`。校验 HTTP 状态、业务状态和响应结构。

提醒须同时满足：

```text
type == "互动课堂" && newsModel == "chat" && newsUrl == "TO_CLASS"
```

该事件作为上课提醒；`TO_CLASS` 是路由标记，不是链接。`isRead` 不参与新消息判断。

## 状态与推送

- 首次完整拉取建立基线，不推送历史；之后按 `(account_id, notification_id)` 去重，同 ID 更新不重推。
- 完整分页；排序未确认前，不因遇到已见记录而停止。请求失败或触及页数上限不提交完整扫描进度。
- 已见通知与待发送任务在同一 SQLite 事务中写入；重启恢复基线、去重记录和未完成任务。
- 企业微信发送须检查业务响应；失败有界退避重试，过期停止。待发送任务不受课程窗口限制；Webhook 响应丢失时仍可能重复投递。
- 会话文件权限 `0600`，密码不落盘；日志隐藏 Token 和 Webhook 密钥。同一状态目录只运行一个实例。

## 配置与命令

TOML：学期、节次、调停课、窗口模式与偏移、轮询间隔、分页、超时、重试、提醒有效期。会话、数据库与 Webhook 凭据不入 Git。

`buptwc` 子命令：`login`、`logout`、`import <file.xls>`、`courses`、`plan`、`check`、`notify test`、`status`、`run`。`logout` 仅清除会话。

**待验证：** Refresh Token 协议；`newsCopyPersonId` 与登录 `user_id` 的对应；角色请求头；分页排序；通知时间语义与有效期。

**验收：** XLS 正确展开、窗口正确合并、首次不刷历史、多页不截断、重启不新建重复任务、发送失败可恢复。





## 参考项目

- https://github.com/byrdocs/bupt-auth
