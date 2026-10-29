# T139 最小配置与只读诊断入口验收

2026-10-29，T139 已完成限定 Linux/amd64 验收。机器索引（本地原件 `../../experiments/gost-mihomo/minimal-cli-validation.json`，不随Git上传）绑定源码、构建、真实 VM、完整证据与独立审查摘要；这不代表 T054 `init`、T058 应用接入或整个 P1 完成。

## 已交付行为

- `hoopa config plan --input PATH --output PATH` 使用现有本地 YAML/JSON 意图解析和同一 config.plan 用例，成功后将完整 Plan 写入调用方指定的新 0600 文件。不覆盖文件、链接或 FIFO，不创建父目录，不启动服务、联网或修改内部状态。发布前文件已完整写入并同步；发布后的错误保留完整文件供调用方检查。
- `hoopa config apply --plan PATH --confirm DIGEST [--accept-disruption] [--request-id ID]` 消费严格 JSON Plan，保留原 revision、摘要和 request ID，复用既有变更/回滚/精确重放。不自动重规划或启动管理服务。
- `hoopa doctor` 和 `api v1 doctor.get` 只读报告 state/configuration/service/core/collector/operations 六类事实。缺实例可同时意味着从未初始化或历史丢失，报告 unavailable，不擅自初始化；诊断请求成功不表示所有组件健康。collector 明确未实现。
- 只读操作的 request ID 仅关联响应，不进入变更去重、接纳或持久操作历史。status 仍保持既有只读边界。

初始契约见 [minimal-cli-v1](../../specs/001-hoopa-proxy-manager/contracts/minimal-cli-v1.md)，有限诊断结构见 [doctor-v1](../../specs/001-hoopa-proxy-manager/contracts/doctor-v1.schema.json)。机器接口的其余操作由 US6 后续扩展。

## 真实旅程

| 场景 | 实际断言 | 普通 / race |
|---|---|---|
| 缺实例 | 两种 doctor 入口给出有限不可用诊断，status 明确失败，完整 HOME/XDG 树不变 | PASS / PASS |
| 显式草稿、提交与离线重放 | 新 Plan 文件；精确错误码拒绝覆盖、篡改、缺授权/绑定；真实 CLI Apply 后两应用线路正确；管理器停止、绑定/核心暂不可用时仍返回相同历史收据，重启不增加核心 | PASS / PASS |
| 候选端口占用与完整回滚 | APPLY_FAILED 与持久 rolled_back 一致；原长连接确实中断，完整旧入口恢复，外国监听器存活，管理器重开后保留恢复实例；公共 Stop 与原进程退出可核验 | PASS / PASS |

每批3个实际测试、0失败/0跳过；各53次真实 CLI 子进程调用，其中41次成功、12次为预登记拒绝/失败响应。普通/race VM 用时52.714/123.586秒，Guest/QEMU均退出0，无 race 报告、强制 VM 清理或应急测试清理。应用 token 数按场景为0/4/5；两个带网络夹具的场景各 non-app=0。入口标签只证明命名应用上下文，不证明原始 OS PID。

每批保留266项完整证据；普通307841字节、race307842字节。仅2份核心和3份 CLI 副本按精确固定 SHA 排除二进制内容，其余普通文件均保留完整内容/摘要，特殊节点只保存类型和模式且不重建。核心固定为 `6ec6a329801a49e69ead6c093955a07c4d1098bdfb26b0b611eae0b5205f745b`。两批共30份实际 doctor 响应另经离线 Schema 校验。

最终2768份构建输入和 SHA 命名原件无漂移。最初 CGO1 列举未带 `-race` 的2750清单保留；在 race VM 前补充18份实际竞态构建专用输入。此修正不覆盖旧清单，也不把初始清单冒称完整。普通驱动/CLI均静态构建；race驱动/CLI的动态库、加载器与符号要求已单独核对。

## 模块证据与边界

control普通/race各4测试，service各23顶层/62测试，CLI各27顶层/84测试，独立输出模块各5顶层/18测试通过；相关vet通过。这些集合存在包含关系，不能相加当独立测试数。32个合成诊断向量与3份Schema meta检查另列，不能替代实际旅程。

测试定义先于实现，旧缺CLI预期RED、夹具期望错误和日期校验器配置错误均保留。独审发现的测试服务回收旧PID重复使用风险已修正：原pidfd退出后核对原启动实例/父级/僵尸状态，仅一次精确Wait4，之后只读观察；拒绝场景也已固定具体错误码。最终审查针对稳定代码与两批原件。

这些旅程手工准备了 SourceSettings、应用绑定和本地 Provider，尚不是完整首次安装。无GOST/SQLite/TUI依赖，也没有交付流量账本、进程级历史、真实应用run或其他平台支持。输出故障注入不等于验证所有真实文件系统故障或突然退出后的临时文件清理。后续继续 T052/T053 接入测试定义，再完成显式初始化与子进程实现。
