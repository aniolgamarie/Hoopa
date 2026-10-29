# US5：基础代理生命周期、配置应用与恢复验收

本页对应 T034，记录实际产品适配器在 Linux/amd64 上的分批验收。所需真实场景、原件和最终覆盖复核已完成，T034 已验收；[汇总索引](../../experiments/gost-mihomo/p1-us5-lifecycle-acceptance.json)绑定各批次及最终审查。每批绑定自己的源码、配置、二进制摘要及原件；这些结果不等于一次针对全部最新文件的完整测试运行。

基础模式使用固定 Mihomo 核心和两个完整入口配置，不依赖 GOST、SQLite 或 BPF。固定核心 SHA256 为 `2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb`。测试通过实际 CLI、管理服务、文件存储、原进程句柄、连接归属证明与独立上游节点见证检查结果。

## 场景与证据

| 范围 | 实际验收内容 | 记录 |
| --- | --- | --- |
| F01 | 固定 CLI 最小前端正常与异常退出；原核心、原连接及新连接继续工作 | `harness-lifecycle-02` / `harness-lifecycle-race-02` |
| F02 | 实际管理器 SIGKILL；原核心身份不变，原长连接与新连接继续转发，计量保持 unavailable | 最终会话接线共享路径的 `harness-session-f02-01` / `harness-session-f02-race-01` |
| F03 | 控制服务重开不修改持久权威；显式 Recover 接管原核心，不增加配置版本或重复启动 | 与 F01 同批；另有 control-only 空／运行状态检查 |
| F04 | 专用 VM 的真实 SSH 断开、交互式登录 shell logout、deep S3 休眠恢复 | S3 `vm-suspend-01` 与 SSH/logout `vm-ssh-02` 均通过执行及独立原件复核 |
| F06 | 仅授权时最多重启一次；耗尽预算或无授权时不重复启动 | `harness-fault-01` / `harness-fault-race-01` |
| F10 | 实际入口绑定冲突；候选失败后完整恢复旧配置、版本及实际线路 | 与 F06 同批 |
| F11 | 17 个发布／运行切点，包括未接纳候选、日志阶段、旧核心停止、启动登记、入口开放与 degraded 发布 | publication-six、runtime-ten、degraded-publication 三批分别普通／race 通过 |
| F12 | active 临时写、文件同步、rename、目录同步切点失败；重新证明同一目标后显式恢复 | active-io-four 普通／race |
| F13 | 回滚核心验证失败，以及回滚 active 写入失败；保存各自真实诊断，阻止普通写入 | rollback-two 普通／race |
| F14 | birth 启动身份不符、缺 birth/registration、不完整 registration、外来监听者与真实内核 PID 复用；拒绝误接管、误杀与重复启动 | 元数据和外来监听者普通／race；真实 PID 复用专用 VM |
| F18 | 固定构建与子进程环境缺少可选组件时，基础启动、转发与停止仍可用 | optional-components-fixed-fixture 普通／race |
| C01 | 实际容量耗尽下拒绝新接纳、旧请求精确回放；同 ID 修改五类字段均拒绝 | `harness-capacity-02` / `harness-capacity-race-03`，分别固定 195／197 份输入 |

除上述场景，还执行了 Stop 意图落盘后管理器死亡、启动前／启动后未登记窗口，以及控制服务单独重开的直接检查。准备阶段与组件测试、空 dispatch、失败批次中的单个 PASS，均不替代这里的实际场景。

可追溯索引：

- 统一适配器四场景（本地原件 `../../experiments/gost-mihomo/p1-product-harness-validation.json`，不随Git上传）
- 持久化、归属、容量与生命周期增量（本地原件 `../../experiments/gost-mihomo/p1-product-fault-increment-validation.json`，不随Git上传）
- 实际会话与 S3（本地原件 `../../experiments/gost-mihomo/p1-product-session-validation.json`，不随Git上传）
- 真实 PID 复用（本地原件 `../../experiments/gost-mihomo/p1-product-pid-reuse-validation.json`，不随Git上传）
- [实现、故障与历史限制](p1-traced-faults.md)

## 判定口径

管理器退出后，公开只读状态可能返回 `unknown / activation_requires_management`。验收不会调用 Recover 来美化查询结果，也不把独立观察到的核心提升为已经激活的管理权威。会话连续性必须另行证明同一强身份、generation、连接到的 controller 归属、两个 ready 入口，并完成原长连接和新连接的实际 payload/echo。SSH 客户端的 Wait 状态只能说明该客户端退出；远端管理器退出由它原来的 pidfd 与完整认证事件流 EOF 独立证明。

每次 Observe、status 与证据读取均保持只读。会话事件前后比较完整持久文件、操作记录、启动及产品信号清单；重开控制服务本身不能成为恢复、启动或配置应用。日志持久化失败时停止后续写入：回滚 active 写入失败可以留下 `rolling_back/pending`，不能伪造成已落盘的 `degraded`。

容量场景保留四个真实操作及 1,020 条明确标注的 Prepared 元数据种子，完整枚举 1,024 条记录。新 Apply 首先受到 pending 恢复要求阻止；允许 pending 的新 service.stop 才到达容量拒绝。种子不是成功业务操作，不具有核心启动或信号效果，旧 ID 回放与内容冲突仍分别验证。

真实 PID 复用在隔离 guest 中发生：同一 boot 的 PID 132 启动 tick 从 1352 变为 2066，Recover 返回 CONFLICT，新的监听者仍活着。它与修改 birth 元数据的场景分别保存。guest 无网卡、无宿主目录共享；宿主 PID namespace 未修改。QMP quit 只是导出后的 VM 收尾，不算 guest OS 关机验收。

所有实际核心都需核对原 pidfd 退出及 `/proc` 消失。保留 pending/degraded 或容量满的故障场景，可能由独立夹具最后发信号收尾；索引单列这些信号，不算产品 Stop 成功。未调用 Start 的 intent 单列，不编造 PID 或退出事实。

## 失败记录与适用范围

原失败批次保留，包括早期生命周期批次、发布／归档错误、容量 race 超时以及首轮 SSH/logout 断言失败。SSH 首轮确实完成了会话事件，但只测到断开前的流量；旧断言错误地要求公开 unknown 时独立 Core 也必须不可观测，因此整轮不算通过。第二轮只修改 F04 断言，继续保留公开 unknown，并要求完整独立证明、零修改和后续真实转发。

结果限于 Linux/amd64、固定核心、固定 TCP 入口与所列故障。SSH 使用专用 guest 的 loopback、PTY、无 PAM 配置，不代表所有发行版的 logind/PAM/终端退出策略。S3 与 PID 复用 VM 使用普通构建，没有冒称 VM race 通过。宿主 race 检查只覆盖实际加了 race 的 Go 监督／probe 组件，固定核心和普通 CLI 不因此自动获得 race 覆盖。

payload 字节只用于验证连接与线路连续性，不是累计流量账本、Token、供应商账单或延迟指标。此阶段没有交付完整 TUI、订阅策略、正式历史计量、跨平台支持或性能预算验收；T034 完成也不等于 P1 或整个项目完成。

SSH/logout 第二轮为 201 份冻结输入、3 PASS 节点、2,875 个导出文件、2,734 份非空原件；七个空引用占位字段明确不可用。只读 checker 的 16 个反例检查通过，首轮失败仍被拒绝，3,834 个原运行／镜像文件的内容、权限、mtime 与成员清单均未改变。S3 使用较早的 197 份输入，其成功分支当时没有执行新版无条件 noMutation 断言；复核人员另对保留的事件前后原件逐项比较五个维度，全部一致。这是既有 S3 证据的离线比较，不是重新执行 S3。
