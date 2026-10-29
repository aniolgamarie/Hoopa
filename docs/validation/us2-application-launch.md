# US2 应用启动与 CLI 有限验收

日期：2026-10-29。T056、T057 已在 Linux/amd64 的下述范围验收，任务累计 72/140。T058 的真实 Agent 与完整首次配置验收单独记录，不能因本页的受控应用通过而提前勾选；P1 和整个项目仍未完成。

## 已交付行为

`hoopa run --app ID [--mode managed|inherit|direct] [--meter] -- ARGV...` 直接执行参数数组，保留应用的 stdin/stdout/stderr，不给应用输出套管理 JSON，不经 shell 展开。普通退出码与实际信号分别保存；应用正常退出126/130不会被误标为 exec 失败或 SIGINT。真正的无效可执行格式返回126和固定 `EXEC_FORMAT`，没有 shell 回退。

`managed` 读取已提交的应用配置，核对原始 bundle、应用绑定、生成入口和来源设置，再通过认证的管理连接与独立原核心身份检查验证入口。只有证明这条入口连接属于该核心之后才发送入口认证。每次 run 不会启动服务、重新选择节点、分配端口或重载核心。`direct`／`inherit` 可以读取已初始化的草稿，不需要服务；已有提交配置损坏时不退回草稿。模式覆盖只影响此次子进程。P1 的计量请求仍明确拒绝，不降为不计量运行。

`hoopa app inspect ID [--mode …] [--meter]` 只读显示声明与此次选择的模式、bypass、环境变量增删名称、入口及配置身份和启动历史。它不认证探测网络，入口标为 `not_probed`；输出不含入口密码、完整 argv 或完整环境。这是本地报告，未向管理 RPC 增加任意进程执行能力。`direct` 仅清理声明的子环境变量，不承诺绕过 TUN、VPN 或应用自己的代理配置。

启动历史采用独立的、有界私有目录和分配锁，不争抢配置管理器的全局写锁。先持久化 reservation，再启动固定 helper；原始 pidfd 与 birth 确认后，持久化授权，最后通过匿名管道发送一次放行。argv/environment 采用有界原始字节载荷，不写入磁盘。管道提前关闭或放行格式错误不能执行用户应用。记录发布失败不会伪装成功；终末落盘失败返回5，并保留已经观察到的原始退出信息。

TTY 前台切换在原 pidfd 与 birth 确认后、放行前进行。观察原始子进程退出时先不回收，保留其 zombie 身份进行前台恢复，然后唯一 Wait 并关闭句柄。wrapper 接收的 INT/TERM/HUP/QUIT 仅转发给原始直接子进程，不按数字 PID 或进程组广播。完整 shell job control 和外部进程恶意竞争终端不在此范围。

根应用结束不等于所有后代和连接结束。当前显示 `descendants=unknown/not_enumerated`，managed 入口租约保持 `unknown`，不自动回收。启动根 PID/boot/start 是应用启动实例信息，不能冒充实际发起 TCP 的进程归属，也不提供流量账本。

## 实际验证

固定 Go1.27.3，实际 `cmd/hoopa` 和独立客户端分别构建普通及 race 版本。核心为单独批准的 app-routing-v2 制品 `6ec6a329801a49e69ead6c093955a07c4d1098bdfb26b0b611eae0b5205f745b`。宿主隔离临时目录的真实 `/proc`、pidfd、loopback 和 PTY 检查可用，本批未使用 VM；未修改既有代理或系统网络设置。

| 最终实际 CLI 场景 | 普通 | race | 验证内容 |
| --- | --- | --- | --- |
| 参数、管道 IO 与 exec 格式失败 | PASS | PASS | 字面参数/二进制输入、direct/inherit、退出17、真实 ENOEXEC126、无 shell 副作用和持久化秘密 |
| 原始子进程信号 | PASS | PASS | 正常130与 SIGINT 区分；INT/TERM 转发；同组独立 sentinel 存活 |
| 独立真实 PTY | PASS | PASS | 三个真实 TTY 描述符、前台读写、Ctrl-C、原前台及 termios 恢复 |
| 后台后代 | PASS | PASS | 根退出17时后代仍持有 stdio；wrapper 不等 EOF；退出后第二次请求仍走原 A-US |
| 已提交 managed 入口 | PASS | PASS | 新 app-b 走 B-JP、原 app-a 继续 A-US；拒绝缺失入口/非法计量，无隐式重载、迁移或核心身份改变 |

每种构建均 5 个顶层 PASS、0 FAIL、0 SKIP，无 race 报告或紧急清理回退；分别观察到27次管理 CLI 调用、11条终末记录和5个真实应用 GET，非应用 origin 请求为0。十份完整现场归档逐文件校验 SHA，包含实际核心/CLI/客户端原件，未排除普通文件。普通1594项、race1634项构建输入分别冻结，最终无源码漂移。

相关组件另有普通/race定向验收：存储与阶段类型最终15顶层/81含子、配置捕获4/18、载荷4/34、实际 runner6/19、CLI16/60。原始进程/TTY组件为5/8；其即时前台切换夹具不能替代本批产品延后 handoff 的实际 PTY 结果。集合有重叠，不相加声称全库测试数。定向 vet 和稳定源码/证据独审通过。

## 保留的失败与边界

最初两个 PTY 轮次失败。第一轮驱动未保存自己的失败 stderr/status；补证后的第二轮确认独立 Python 观察器关闭自身控制终端时收到 SIGHUP，raw wait status 为1。即使当时应用终末为17，也没有将失败改标通过。修订只让观察器在完成产品断言和 wrapper wait 后处理自身收尾的 SIGHUP；没有改产品或弱化 TTY 断言。最终普通/race两套完整场景重新通过。旧失败日志、状态和完整归档均保留。

本批支持 F15/F18/F19 的受控应用部分，不等于真实模型服务访问、完整外部客户端兼容性、UDP/QUIC计量、全后代清理或整机流量审计。其他平台、资源预算、生产部署和完整 P0/P1 未验收。

脱敏机器索引：us2-launch-validation.json（本地原件 `../../experiments/gost-mihomo/us2-launch-validation.json`，不随Git上传）。私有原件入口：`private/us2-preparation/t056/`、`private/us2-preparation/t058-runtime/receipt.md`、`summary.json`、`final-review.md`。事务与范围裁决见[启动记录](../../specs/001-hoopa-proxy-manager/contracts/application-launch-records-v1.md)和[执行屏障](../../specs/001-hoopa-proxy-manager/contracts/application-launch-dispatch-v1.md)。主代理负责时序与最终验收；executor 使用 gpt-6.1-sol/medium，scout 使用 gpt-6-luna/high。
