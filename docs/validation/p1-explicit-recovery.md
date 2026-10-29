# T031：显式恢复与独立调用记录的有限验收

2026-10-27。`BasicApplier.Recover` 已完成有限模块验证：重新核验已开放的目标或回滚
实例，补齐 active/原操作终态，再分别完成恢复调用的记录。真实核心上验证了三个
场景；最终受影响四包 race 1037 pass、0 fail/skip，vet/格式/空白检查通过。
这些场景没有杀管理器 OS 进程；后续[真实 SIGKILL 补验](p1-manager-crash-recovery.md)
单独留证。完整产品验收仍由 T034 独立执行。

实现见 [recover.go](../../internal/control/recover.go)、
[激活事实核验](../../internal/mihomo/activation_proof_linux.go) 和
[显式临时文件维护](../../internal/config/operation_maintenance_linux.go)。机器索引见
p1-explicit-recovery-validation.json（本地原件 `../../experiments/gost-mihomo/p1-explicit-recovery-validation.json`，不随Git上传），
顺序约束见[基础替换与恢复契约](../../specs/001-hoopa-proxy-manager/contracts/basic-apply-v1.md)。

## 恢复行为

新 `operation.recover` 请求有独立 request_id，params 精确包含原 operation_id 和
expected_revision。相同请求只重放已保存结果；换内容复用 ID 拒绝。新请求先验证
revision、原目标和所有未完成操作，只有准确关联同一原目标的旧恢复调用可以一起
收尾。关联绑定原目标完整请求摘要和恢复请求参数，原 apply 身份与原件不被替换。

恢复调用先持久到 applying，再检查原目标。核心核验使用新取得并完整匹配出生
身份的 pidfd、连接所属、完整快照和精确激活回执。发布前后都核验同一实例；这条
恢复路径不启动核心、不发送 activate、不发送 TERM、不重新构造一次性启动票据。

已保存的退出事实必须有对应旧 intent、birth 和本 apply 操作的 open registration，
完整 Match 且唯一对应 base。回滚候选的退出事实同样要求其 closed registration 和
出生原件。缺失、损坏、错 gate、错运行或歧义，均不能仅凭 OldStopped/TargetStopped
布尔值通过。重核的是持久身份原件，不对预期已退出的 PID 再做 Observe 或发信号。

原操作实际核验终态后，恢复调用再单独提交。两者之间中断时，原操作可已完成而
调用仍 pending；新的恢复请求重新核验原实例，再完成准确关联的旧调用。没有把
两份文件与核心视为共同事务。持久写失败停止后续步骤；不确定 writer 不能继续。

临时残留维护只在新显式恢复请求通过校验后运行，持有 writer 锁，先完整核对名称、
权限、单链接文件、大小与数量，再删除严格 `.operation-48hex` 未发布文件并同步父目录。
权威 `.json` 不删除；查询、同请求重放和普通打开不维护。删除部分成功或同步失败
保留计数并 poison writer。该 API 的实际文件系统/故障注入专项为 26 pass。

失败响应遵循完整 API Response：ok:false、data:null，error 含 code/message/retryable，
details.operation_id/reason 保留查询关联。终态 phase 与内部 RunID 存在原操作/evidence
中，不加入公开 MutationReceipt。最终回归覆盖这次响应修正；旧验收包保持旧源码。

## 实际核心场景

全部使用固定 Linux/amd64 basic build
`2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb`。

| 场景 | 实际证据 |
| --- | --- |
| active 已发布，原操作未写终态 | PID 1939490；关闭并重开 Coordinator/Store，公开 Recover 重新核验同 PID/RunID/激活事实，原操作和调用完成；实际本地转发成功 |
| 原目标已完成，恢复调用尚未完成 | 新场景 PID 1939586；在两记录之间切断，再次关闭/重开，以新请求完成旧调用；原目标身份不变，没有新增运行 |
| 自动回滚完成后的恢复核验 | 旧 PID 1939686、候选 1939783、恢复实例 1939826；公开 Recover 实际核对旧/候选身份原件与当前恢复实例；原 rolled_back 记录 SHA 不变，新调用 committed |

三个场景共 5 次核心 exec。测试节点为 3 顶层、2 子测试，5 pass：其中 3 个实际
场景、1 个空 launcher helper、1 个聚合父测试，不能解释为 5 个独立场景。
同请求重放无新增 source 调用、运行或状态树变更；所有本次核心均通过测试保留的
独立原 pidfd 核验清理，并观察 /proc 消失。未宣称取得产品内部 child Wait 的独立退出码。

这里的重开是同一测试父进程内的 Coordinator/Store 关闭和重新构造；恢复使用新
所有权观察，不继承旧 OwnedRun。没有向管理器 OS 进程发送 SIGKILL，也没有测试
SSH 注销、宿主休眠或 IPC 服务重启，不能冒充这些生命周期场景。
前两个单核场景未另存重定向 launcher.log；三实例场景保留日志大小/摘要，完整测试
stdout 与固定源码原件已封存。初次测试编译类型错误及旧守卫的历史测试记录保留。

## 最终稳定源码验证

| 包 | 顶层 | 子测试 | pass |
| --- | ---: | ---: | ---: |
| config | 66 | 402 | 468 |
| control | 46 | 305 | 351 |
| runtime | 19 | 59 | 78 |
| mihomo | 19 | 121 | 140 |
| 合计 | 150 | 887 | 1037 |

最终 race 与 vet 使用稳定输入，源码/module/公开 Plan 向量/指定核心原件前后哈希
一致。已经单独执行的实际核心测试通过 `-skip` 明确排除，不计入以上分母；材料桥
仍用指定固定 ELF 做实际只读核验，因此本轮没有默认 opt-in skip。81 个相关 Go 文件
gofmt 与空白检查通过。独立恢复定向新版另有 42 pass，覆盖两记录中断、同 ID 重放、
新调用关联收尾、旧/候选身份原件缺失或错配、歧义、未证退出、拒绝前零维护和降级。
该定向使用合成 verifier 与真实 Store，不能替代实际 pidfd 证据。

主代理负责实现和裁决恢复语义；executor 实际 gpt-6.1-sol / medium，实施限定维护 API
及测试；scout 实际 gpt-6-luna / high，独立审查稳定变更和证据。原退出原件缺口由
审查发现并在最终版本修复，早期 29-pass 合成结果不用于证明新增守卫。

## 适用边界

只完成已开放且身份完整可证的实例。closed、启动/退出未明、缺 birth/registration、
prepared 空证据、丢失一次性启动票据或不完整回滚，都保留 degraded，阻止自动新写。
它们没有被报告成零副作用或成功恢复，也不通过缺 PID 推断退出。新恢复暂不接受
已经压缩正文的目标；身份容量达到 1024 时按既有容量规则拒绝新的恢复请求。

自动回滚与当前显式恢复的模块范围已具备受限证据；历史 stopped/受控重启、生产
服务/CLI 与完整 F10–F14 矩阵继续分别由 T032、T033、T034 负责。实际管理器死亡的
单场景证据见[后续补验](p1-manager-crash-recovery.md)，不计入本页原始 3 场景/5 核心分母。
没有跨平台或完整 P0/P1 完成声明。
