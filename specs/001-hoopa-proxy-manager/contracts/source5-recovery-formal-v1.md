# T128 source5 正式恢复矩阵 v1

2026-10-26。固定0017核心 SHA256 `5693d4ac2bda5fcae7e5ac037d23db1b2a48049f681b8c825b8b4db12ac1b967`，以前述 [source5分阶段恢复契约](source5-recovery-transport-v1.md)和已接受的准备证据为输入。每个准备样本保留原阶段；本契约不把准备改判为正式，也不单凭规划完成T128。

| Family | 正式case | VM | 原应用槽 |
| --- | --- | --- | --- |
| transport | offline-drop-closed、offline-reject-closed、held-opened | 3 | 6 |
| restart | combined-reopen | 1 | 2 |
| enospc | source-write-enospc | 1 | 2 |
| quota | logical-quota | 1 | 3 |
| term | active-term | 1 | 1 |
| buffer | buffer-conflict | 1 | 2 |

合计8VM/16原应用槽。buffer的两次collector控制请求单列，不增加原应用分母。各槽ID为`formal-<family>-NNN`，全矩阵唯一；核心配置generation与新随机seed派生的capture generation均独立于准备样本。固定核心、collector、BPF、原始身份观察与故障/对账顺序不因formal标签改变。

## 注册与运行

六family各自在独立目录形成新镜像，通过适用准备检查、完整校验器输入和实际cpio隔离导入闭包，冻结后由主代理验收。实际register/run/check入口只接受formal；纯fixture可保留preparation支持以验证历史合成输入，不能借其结果补正式槽。

第一VM之前必须完成全部六份不可覆盖family registration和统一root matrix registration。后者绑定各family、每case的精确slot集合、镜像及代码冻结引用，固定16分母。主代理在每case启动前再次核对全部注册和根manifest，保存不可覆盖execution admission，记录根manifest引用、case、调用argv、所有注册已验证和该case尚未claim。family driver继续核验自身镜像/注册/claim。8VM串行，每场最多2CPU/1GiB/150秒，无NIC、无宿主共享；BPF/cgroup/故障操作仅在自有guest中。

注册数、执行数、通过数分别保存；VM未启动、提取失败、校验失败不能计入通过或用其他case补位。原件不修复、不截断，不对旧失败重新评分。修订必须由主代理按具体原因裁决新版本/新注册，保留旧完整分母和记录。

## 必须保留的分层结论

- transport三场验证明确cutoff内全部来源原文/终末及receipt；关闭时marker是否送达另记，不能提升为通用退出交付保证。
- combined-reopen保持旧非零12/12检查点、collector/core依次SIGKILL、新实例/epoch及旧原文同receipt重放、新流33/33。无writer结束证明的只读报告仍pending；同一实际run输入的固定源码私有副本三次独占重开另外验证稳定gap身份及原文不变。
- enospc必须有真实source writer FD的内核ENOSPC，与逻辑预留和注入分开。实际完整尾或partial尾决定预登记恢复分支；partial须安全拒绝、不修复。对同一实际run的old-source私有副本运行固定源码overlay，不能用generic退出码独自证明内部错误类型。未观察的真实尾部分支保持未验证。
- quota验证200000字节全局预留不足、新流拒绝、原流终末保留及释放后新准入；不称作永久容量耗尽或每秒worker窗口耗尽。
- term在12/12实际checkpoint和receipt之后发送TERM并wait，再授权原客户端只recv。真实Close先发生时接受closed12/12和complete/unfinished0；source shutdown先发生时接受明确shutdown gap与null final/tail、不完整epoch。不得强造gap。
- buffer验证node额外8字节与目标/应用/source/segment的33/41对33/33守恒、冲突409无receipt及原文204同receipt/journal不变。应用send调用形状不等于TCP包边界；实际客户端预读为空时明确记录，不冒称命中预读分支。

固定源码race继续单列：整数超过2^53、154次直接checkpoint后窗口缺口、partial Write实际n的注入、full/partial/Sync uncertain失败边界、HTTP缓冲net.Pipe及lazy ready_unknown。它们不增加正式VM槽，也不冒称真实物理故障/周期worker/网络分包。

## 只读总入口与后续

`experiments/mihomo-events/recovery_test.go`通过`HOOPA_T128_ACCEPTANCE`及`HOOPA_T128_ACCEPTANCE_SHA256`显式消费主代理接受的完整证据。两项均缺为unverified skip，部分缺失为fail。Python总gate只读取并核对固定manifest、同核心身份前置、6注册/8case/16唯一slot、每次执行前根登记绑定、原运行镜像/完整canonical exports、两个实际无skip恢复overlay、固定源码边界及失败历史；不执行decoder/证据代码，不启动VM、网络或修复账本。成功只代表已独立审查证据的完整性，`new_runtime_samples=0`。

最终主代理裁决另外绑定稳定报告和根登记，满足后才勾T128。T013消费同一组run形成G02评审，旧GOST完整标签series/浮点边界另引历史证据，不再运行新source5样本。T129质量线路、SQL驱动/事务、生产GC/gap持久化、跨平台和性能预算仍按后续任务完成；T128通过不放行P0/P1。
