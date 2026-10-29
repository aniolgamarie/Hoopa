# T139 最小配置与诊断入口

2026-10-29 root 决定，实施中；测试或源码存在不代表本项已验收。T051 已通过，T054 初始化与 T058 完整首次旅程仍是后续任务。

沿用现有 CLI 的 JSON 响应格式，让普通参数命令调用相同的版本化服务用例。`hoopa api v1 OPERATION` 的单请求/单响应边界不变；此阶段不增加交互提示或另一套配置、重试、回滚状态机。

- `hoopa config plan --input PATH --output PATH`：读取有界本地 YAML/JSON 意图，复用 config.plan。成功时输出原 Plan 响应，另把完整 Plan 保存到指定新文件，权限 0600。不能覆盖已有文件或链接，不能写 FIFO；临时文件写入/同步/独占发布必须有界。用户输出是唯一新增持久写入，失败不得生成半份可见 Plan 或内部状态；发布后的同步错误保留已发布原件，不伪装未写入。没有目录时拒绝，不创建输出目录。最终输出目录须由当前用户或 root 控制；非 sticky 的组/其他用户可写目录拒绝，避免临时文件名被他人替换。输出目录按实际打开的目录句柄锚定，不沿用内部 Store 对所有祖先目录的额外限制。
- `hoopa config apply --plan PATH --confirm DIGEST [--accept-disruption] [--request-id ID]`：读取有界 JSON Plan，复用 config.apply 与原 Plan 的 base revision，不自动刷新、下载、重规划、启动管理服务或接受中断。没有确认摘要或无法确认的中断明确拒绝。精确已接纳请求继续走既有持久重放；历史 Plan v1 只保留原精确重放含义。
- `hoopa status`：复用已有只读状态。没有可读实例时返回既有明确错误，不能自动初始化。
- `hoopa doctor` / `doctor.get`：参数为空，读取当前可验证状态并给出有限建议。建议不执行，不探测外部网络、不写 last_seen/日志/数据库、不获取写锁、不修权限、不补目录。现有本地核心身份查询可用于已有 status 证明，不能冒充主动业务延迟或健康探针。

已实现只读操作统一识别为 status.get、doctor.get、operation.get、subscription.list、config.plan；request_id 仅关联响应，不读取变更去重条目来决定是否允许查询，不因与变更历史 ID 相同而重放或拒绝查询，也不写新的操作记录。写操作继续先精确 Lookup，再校验新操作；未知操作不因此获得只读权限。

Doctor data 的有限版本为 `{schema_version:1, observed_at, checks}`，checks 按 state、configuration、service、core、collector、operations 固定顺序。每项为 `{id,result,evidence,reason,recommendation}`；result 取 pass/warning/unavailable。reason 与 recommendation 来自固定枚举，不包含 OS 错误、文件正文、凭据、任意路径或环境变量。schema_version 仅描述诊断投影，不扩展当前核心能力。

证据分为 protected_local_files、durable_operation_journal、current_management_service、fresh_core_identity、not_observed。当前collector明确 unavailable/not_implemented。配置没有 committed generation 时 warning/no_committed_configuration；不能把草稿当作已应用配置。核心只沿用已证运行/已证退出，否则 unavailable 并保留不知道的原因，不显示零流量/零延迟。

安全读取遇到明确缺失原件时，doctor可成功返回诊断，state=unavailable、reason=state_missing_or_incomplete，其他需要该原件的项目均not_observed。该结果不区分全新实例与丢失旧文件，不建议覆盖旧状态。权限/完整性/未知IO失败仍返回原错误信封；取消保持取消，不伪装成功诊断。diagnostic响应成功只表示检查完成，不表示所有检查通过。

T139 实际CLI测试可用已实现的显式 source settings/app binding 初始化接口准备前置材料；必须标明这一点。真实命令通过独立构建的 cmd/hoopa 子进程执行，服务由正常CLI控制，不能以 test-dispatch 或 cli.Execute 模拟完整首次旅程。初始无实例、有显式本地草稿、成功提交和失败后完整回滚分别验收；完整 `hoopa init` 的第一次安装接入仍由 T054/T058完成。
